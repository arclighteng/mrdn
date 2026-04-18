import type { ParsedQuery, Filter, CompiledQuery, Cursor } from "./types";

export class ComplexityError extends Error {
  constructor(score: number) {
    super(`Query complexity ${score} exceeds limit of 6. Add type:, ticker:, by:, or date filters to narrow.`);
    this.name = "ComplexityError";
  }
}

export class ScoreDeltaError extends Error {
  constructor() {
    super("sort:score-delta is not yet supported. Use sort:score instead.");
    this.name = "ScoreDeltaError";
  }
}

interface TableMeta {
  name: string;
  eventType: string;
  alias: string;
  dateCol: string;
  hasCompany: boolean;
  hasPerson: boolean;
  amountCol: string | null;
  amountIsCents: boolean;
  filterCols: Record<string, string>;
  customJoins?: string;
}

const TABLES: TableMeta[] = [
  {
    name: "congressional_trades",
    eventType: "trade",
    alias: "ct",
    dateCol: "ct.traded_at",
    hasCompany: true,
    hasPerson: true,
    amountCol: "(ct.amount_range_low + ct.amount_range_high) / 2",
    amountIsCents: false,
    filterCols: {
      action: "ct.trade_type",
      owner: "ct.owner_type",
    },
  },
  {
    name: "contracts",
    eventType: "contract",
    alias: "co",
    dateCol: "co.awarded_at",
    hasCompany: true,
    hasPerson: false,
    amountCol: "co.amount_cents",
    amountIsCents: true,
    filterCols: {
      action: "co.action_type",
      agency: "co.agency",
    },
  },
  {
    name: "sanctions",
    eventType: "sanction",
    alias: "sn",
    dateCol: "sn.added_at",
    hasCompany: true,
    hasPerson: false,
    amountCol: null,
    amountIsCents: false,
    filterCols: {
      country: "sn.country",
      program: "sn.program",
    },
  },
  {
    name: "donations",
    eventType: "donation",
    alias: "dn",
    dateCol: "dn.donated_at",
    hasCompany: true,
    hasPerson: false,
    amountCol: "dn.amount_cents",
    amountIsCents: true,
    filterCols: {},
  },
  {
    name: "lobbying",
    eventType: "lobbying",
    alias: "lb",
    dateCol: "lb.period_end",
    hasCompany: true,
    hasPerson: false,
    amountCol: "lb.amount_cents",
    amountIsCents: true,
    filterCols: {
      registrant: "lb.registrant",
    },
  },
  {
    name: "insider_trades",
    eventType: "insider",
    alias: "it",
    dateCol: "it.traded_at",
    hasCompany: true,
    hasPerson: false,
    amountCol: "(it.shares * it.price_cents)",
    amountIsCents: true,
    filterCols: {
      action: "it.trade_type",
    },
  },
  {
    name: "court_filings",
    eventType: "court",
    alias: "cf",
    dateCol: "cf.filed_at",
    hasCompany: true,
    hasPerson: false,
    amountCol: null,
    amountIsCents: false,
    filterCols: {
      filing: "cf.filing_type",
    },
  },
  {
    name: "warn_filings",
    eventType: "warn",
    alias: "wf",
    dateCol: "wf.layoff_date",
    hasCompany: true,
    hasPerson: false,
    amountCol: null,
    amountIsCents: false,
    filterCols: {
      state: "wf.state",
      workers: "wf.workers_affected",
    },
  },
  {
    name: "tariffs",
    eventType: "tariff",
    alias: "tf",
    dateCol: "tf.effective_at",
    hasCompany: false,
    hasPerson: false,
    amountCol: null,
    amountIsCents: false,
    filterCols: {
      country: "tf.affected_countries",
    },
    customJoins: "LEFT JOIN company_hs_codes chc ON chc.hs_code = ANY(tf.hs_codes) LEFT JOIN companies c ON c.id = chc.company_id",
  },
];

const PERSON_FILTERS = new Set(["by", "party", "branch", "committee"]);

export function compile(
  query: ParsedQuery,
  cursor: Cursor | null,
  signalTickers: string[],
): CompiledQuery {
  if (query.sort === "score-delta") {
    throw new ScoreDeltaError();
  }

  const complexity = computeComplexity(query, signalTickers);
  if (complexity > 6) {
    throw new ComplexityError(complexity);
  }

  const params: unknown[] = [];
  const warnings: string[] = [];
  const needsScoreCTE = hasScoreFilter(query) || query.sort === "score";

  const typeFilter = query.filters.find((f) => f.key === "type" && !f.negated);
  const negTypeFilter = query.filters.find((f) => f.key === "type" && f.negated);

  let tables = TABLES;
  if (typeFilter) {
    tables = TABLES.filter((t) => typeFilter.values.includes(t.eventType));
  }
  if (negTypeFilter) {
    tables = tables.filter((t) => !negTypeFilter.values.includes(t.eventType));
  }

  const branches = tables.map((table) => buildBranch(table, query, params, cursor, signalTickers, warnings));

  const innerSql = branches.length === 1 ? branches[0] : branches.join("\nUNION ALL\n");

  params.push(query.limit);
  const limitIdx = params.length;

  let sql: string;
  if (query.group) {
    const groupKey = getGroupKey(query.group);
    sql = `SELECT ${groupKey} AS group_key, '${query.group}' AS group_by, COUNT(*) AS count,
      MIN(occurred_at) AS first_seen, MAX(occurred_at) AS last_seen,
      SUM(amount_mid) AS total_amount
    FROM (\n${innerSql}\n) AS base
    GROUP BY ${groupKey}
    ORDER BY count DESC
    LIMIT $${limitIdx}`;
  } else {
    const sortCol = getSortColumn(query.sort);
    sql = `SELECT * FROM (\n${innerSql}\n) AS unified\nORDER BY ${sortCol}\nLIMIT $${limitIdx}`;
  }

  if (needsScoreCTE) {
    sql = `WITH latest_scores AS (
  SELECT DISTINCT ON (company_id)
    company_id, composite_score, market_score, policy_score, insider_score
  FROM scores
  ORDER BY company_id, computed_at DESC
)\n${sql}`;
  }

  return { sql, params, complexity, warnings };
}

function buildBranch(
  table: TableMeta,
  query: ParsedQuery,
  params: unknown[],
  cursor: Cursor | null,
  signalTickers: string[],
  warnings: string[],
): string {
  const wheres: string[] = [];
  const joins: string[] = [];

  joins.push(`JOIN events e ON e.id = ${table.alias}.event_id`);

  if (table.hasCompany) {
    if (table.name === "lobbying") {
      joins.push(`LEFT JOIN companies c ON c.id = ${table.alias}.client_company_id`);
    } else {
      joins.push(`JOIN companies c ON c.id = ${table.alias}.company_id`);
    }
  } else if (table.customJoins) {
    joins.push(table.customJoins);
  }

  if (table.hasPerson) {
    joins.push(`JOIN persons p ON p.id = ${table.alias}.person_id`);
  }

  const needsScores = hasScoreFilter(query) || query.sort === "score";
  if (needsScores && table.hasCompany) {
    joins.push("LEFT JOIN latest_scores ls ON ls.company_id = c.id");
  }

  for (const filter of query.filters) {
    if (filter.key === "type") continue;

    const clause = buildFilterClause(table, filter, params, signalTickers, warnings);
    if (clause) {
      wheres.push(clause);
    }
  }

  for (const text of query.bareText) {
    const clauses: string[] = [];
    params.push(`%${text}%`);
    const paramIdx = params.length;
    if (table.hasCompany || table.customJoins) {
      clauses.push(`c.ticker ILIKE $${paramIdx}`);
      clauses.push(`c.name ILIKE $${paramIdx}`);
    }
    if (table.hasPerson) {
      clauses.push(`p.name ILIKE $${paramIdx}`);
    }
    if (clauses.length > 0) {
      wheres.push(`(${clauses.join(" OR ")})`);
    }
  }

  if (cursor) {
    params.push(cursor.occurred_at);
    const tsIdx = params.length;
    params.push(cursor.event_id);
    const idIdx = params.length;
    wheres.push(`(${table.dateCol} < $${tsIdx} OR (${table.dateCol} = $${tsIdx} AND e.id < $${idIdx}))`);
  }

  const whereClause = wheres.length > 0 ? `WHERE ${wheres.join(" AND ")}` : "";

  const select = buildProjection(table, needsScores);

  return `SELECT ${select}
FROM ${table.name} ${table.alias}
${joins.join("\n")}
${whereClause}`;
}

function buildFilterClause(
  table: TableMeta,
  filter: Filter,
  params: unknown[],
  signalTickers: string[],
  warnings: string[],
): string | null {
  const neg = filter.negated ? "NOT " : "";

  if (filter.key === "ticker") {
    if (!table.hasCompany && !table.customJoins) {
      warnings.push(`ticker: filter not applicable to ${table.eventType}`);
      return "1=0";
    }
    params.push(filter.values);
    return `${neg}c.ticker = ANY($${params.length})`;
  }

  if (filter.key === "sector") {
    if (!table.hasCompany && !table.customJoins) return null;
    params.push(`%${filter.values[0]}%`);
    return `${neg}c.sector ILIKE $${params.length}`;
  }

  if (filter.key === "subsector") {
    if (!table.hasCompany) return null;
    params.push(`%${filter.values[0]}%`);
    return `${neg}c.subsector ILIKE $${params.length}`;
  }

  if (filter.key === "market-cap") {
    if (!table.hasCompany) return null;
    params.push(filter.values[0]);
    return `${neg}c.market_cap_bucket = $${params.length}`;
  }

  if (PERSON_FILTERS.has(filter.key)) {
    if (!table.hasPerson) {
      warnings.push(`${filter.key}: filter not applicable to ${table.eventType}`);
      return "1=0";
    }

    if (filter.key === "by") {
      const isName = filter.values.some((v) => v.includes(" "));
      if (isName) {
        params.push(`%${filter.values[0]}%`);
        return `${neg}p.name ILIKE $${params.length}`;
      }
      params.push(filter.values);
      return `${neg}p.slug = ANY($${params.length})`;
    }

    if (filter.key === "party") {
      params.push(filter.values);
      return `${neg}p.party = ANY($${params.length})`;
    }

    if (filter.key === "branch") {
      const roleMap: Record<string, string> = { senate: "senator", house: "representative" };
      params.push(filter.values.map((v) => roleMap[v] || v));
      return `${neg}p.role = ANY($${params.length})`;
    }

    if (filter.key === "committee") {
      params.push(filter.values);
      return `${neg}EXISTS (SELECT 1 FROM person_committees pc WHERE pc.person_id = p.id AND pc.committee_code = ANY($${params.length}))`;
    }
  }

  if (filter.key === "since" || filter.key === "before") {
    const resolved = resolveDate(filter.values[0]);
    params.push(resolved);
    const op = filter.key === "since" ? ">=" : "<";
    return `${table.dateCol} ${op} $${params.length}`;
  }

  if (filter.key.endsWith("-score") || filter.key === "score") {
    const colMap: Record<string, string> = {
      score: "ls.composite_score",
      "market-score": "ls.market_score",
      "policy-score": "ls.policy_score",
      "insider-score": "ls.insider_score",
    };
    const col = colMap[filter.key];
    if (!col || !table.hasCompany) return null;
    return buildRangeClause(col, filter, params);
  }

  if (filter.key === "amount") {
    if (!table.amountCol) return null;
    const amountFilter = { ...filter };
    amountFilter.values = filter.values.map((v) => {
      let num = parseAmountValue(v);
      if (table.amountIsCents) num *= 100;
      return String(num);
    });
    if (amountFilter.upperBound) {
      let upper = parseAmountValue(amountFilter.upperBound);
      if (table.amountIsCents) upper *= 100;
      amountFilter.upperBound = String(upper);
    }
    return buildRangeClause(table.amountCol, amountFilter, params);
  }

  if (filter.key === "workers") {
    if (table.eventType !== "warn") return null;
    return buildRangeClause("wf.workers_affected", filter, params);
  }

  if (filter.key === "signal") {
    if (!table.hasCompany && !table.customJoins) return null;
    if (signalTickers.length === 0) return null;
    params.push(signalTickers);
    return `c.ticker = ANY($${params.length})`;
  }

  const col = table.filterCols[filter.key];
  if (col) {
    if (filter.key === "country" && table.eventType === "tariff") {
      params.push(filter.values);
      return `${neg}tf.affected_countries && $${params.length}`;
    }
    if (filter.key === "agency" || filter.key === "registrant") {
      params.push(`%${filter.values[0]}%`);
      return `${neg}${col} ILIKE $${params.length}`;
    }
    params.push(filter.values);
    return `${neg}${col} = ANY($${params.length})`;
  }

  return null;
}

function buildProjection(table: TableMeta, needsScores: boolean): string {
  const t = table.alias;
  const person = table.hasPerson
    ? `p.slug AS person_slug, p.name AS person_name, p.party AS person_party, p.branch AS person_branch`
    : `NULL::text AS person_slug, NULL::text AS person_name, NULL::text AS person_party, NULL::text AS person_branch`;

  const company = table.hasCompany || table.customJoins
    ? `c.ticker, c.name AS company_name, c.sector`
    : `NULL::text AS ticker, NULL::text AS company_name, NULL::text AS sector`;

  const action = table.filterCols["action"]
    ? `${table.filterCols["action"]} AS action`
    : `NULL::text AS action`;

  const owner = table.eventType === "trade" ? `${t}.owner_type AS owner` : `NULL::text AS owner`;

  const amountMid = table.amountCol
    ? `(${table.amountCol})${table.amountIsCents ? " / 100" : ""} AS amount_mid`
    : `NULL::bigint AS amount_mid`;

  const score = needsScores && table.hasCompany
    ? `ls.composite_score AS score`
    : `NULL::numeric AS score`;

  const filedAt = ["trade", "insider", "warn"].includes(table.eventType)
    ? `${t}.filed_at`
    : `NULL::timestamptz AS filed_at`;

  const agency = table.eventType === "contract" ? `${t}.agency` : `NULL::text AS agency`;
  const program = table.eventType === "sanction" ? `${t}.program` : `NULL::text AS program`;
  const country = table.eventType === "sanction" ? `${t}.country` : table.eventType === "tariff" ? `NULL::text AS country` : `NULL::text AS country`;
  const state = table.eventType === "warn" ? `${t}.state` : `NULL::text AS state`;
  const workers = table.eventType === "warn" ? `${t}.workers_affected` : `NULL::int AS workers_affected`;
  const entityName = table.eventType === "sanction" ? `${t}.entity_name` : `NULL::text AS entity_name`;
  const description = table.eventType === "contract" ? `${t}.description` : `NULL::text AS description`;
  const registrant = table.eventType === "lobbying" ? `${t}.registrant` : `NULL::text AS registrant`;
  const filingType = table.eventType === "court" ? `${t}.filing_type` : `NULL::text AS filing_type`;
  const filerName = table.eventType === "insider" ? `${t}.filer_name` : `NULL::text AS filer_name`;
  const filerTitle = table.eventType === "insider" ? `${t}.filer_title` : `NULL::text AS filer_title`;
  const shares = table.eventType === "insider" ? `${t}.shares` : `NULL::int AS shares`;

  return `'${table.eventType}' AS event_type, e.id AS event_id, ${table.dateCol} AS occurred_at,
    ${person}, ${company}, ${action}, ${owner}, ${amountMid},
    ${agency}, ${program}, ${country}, ${state}, ${workers},
    ${entityName}, ${description}, ${registrant}, ${filingType},
    ${filerName}, ${filerTitle}, ${shares}, ${filedAt}, ${score}`;
}

function getSortColumn(sort: string): string {
  switch (sort) {
    case "score": return "score DESC NULLS LAST, occurred_at DESC";
    case "amount": return "amount_mid DESC NULLS LAST, occurred_at DESC";
    default: return "occurred_at DESC, event_id DESC";
  }
}

function getGroupKey(group: string): string {
  switch (group) {
    case "week": return "date_trunc('week', occurred_at)";
    case "month": return "date_trunc('month', occurred_at)";
    case "company": return "ticker";
    case "person": return "person_slug";
    case "sector": return "sector";
    case "type": return "event_type";
    default: return group;
  }
}

function buildRangeClause(col: string, filter: Filter, params: unknown[]): string {
  if (filter.operator === "..") {
    params.push(Number(filter.values[0]));
    const loIdx = params.length;
    params.push(Number(filter.upperBound!));
    const hiIdx = params.length;
    return `${col} BETWEEN $${loIdx} AND $${hiIdx}`;
  }
  const op = filter.operator || "=";
  params.push(Number(filter.values[0]));
  return `${col} ${op} $${params.length}`;
}

function parseAmountValue(v: string): number {
  const match = v.match(/^([\d.]+)([kmb])?$/i);
  if (!match) return Number(v);
  let num = parseFloat(match[1]);
  const suffix = (match[2] || "").toLowerCase();
  if (suffix === "k") num *= 1_000;
  else if (suffix === "m") num *= 1_000_000;
  else if (suffix === "b") num *= 1_000_000_000;
  return num;
}

function resolveDate(value: string): string {
  if (/^\d{4}(-\d{2}(-\d{2})?)?$/.test(value)) {
    if (value.length === 4) return `${value}-01-01`;
    if (value.length === 7) return `${value}-01`;
    return value;
  }

  const now = new Date();
  switch (value) {
    case "today": return now.toISOString().slice(0, 10);
    case "ytd": return `${now.getFullYear()}-01-01`;
    case "last-week": {
      const d = new Date(now);
      d.setDate(d.getDate() - 7);
      return d.toISOString().slice(0, 10);
    }
    case "last-month": {
      const d = new Date(now);
      d.setMonth(d.getMonth() - 1);
      return d.toISOString().slice(0, 10);
    }
    case "last-quarter": {
      const d = new Date(now);
      d.setMonth(d.getMonth() - 3);
      return d.toISOString().slice(0, 10);
    }
  }

  const relMatch = value.match(/^(\d+)([dwmy])$/);
  if (relMatch) {
    const n = parseInt(relMatch[1], 10);
    const unit = relMatch[2];
    const d = new Date(now);
    switch (unit) {
      case "d": d.setDate(d.getDate() - n); break;
      case "w": d.setDate(d.getDate() - n * 7); break;
      case "m": d.setMonth(d.getMonth() - n); break;
      case "y": d.setFullYear(d.getFullYear() - n); break;
    }
    return d.toISOString().slice(0, 10);
  }

  return value;
}

function hasScoreFilter(query: ParsedQuery): boolean {
  return query.filters.some((f) =>
    f.key === "score" || f.key === "market-score" || f.key === "policy-score" || f.key === "insider-score"
  );
}

function computeComplexity(query: ParsedQuery, signalTickers: string[]): number {
  let score = 0;
  const hasType = query.filters.some((f) => f.key === "type");
  const hasTicker = query.filters.some((f) => f.key === "ticker");
  const hasPerson = query.filters.some((f) => f.key === "by");
  const hasDate = query.filters.some((f) => f.key === "since" || f.key === "before");

  if (!hasType) score += 3;
  if (query.group) {
    score += 2;
    if (!hasDate) score += 2;
  }
  if (!hasDate) score += 2;
  if (query.filters.some((f) => f.key === "signal") && signalTickers.length === 0) score += 3;
  if (!hasType && !hasTicker && !hasPerson && !hasDate) score += 2;

  return score;
}

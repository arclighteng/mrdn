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

export class CursorSortError extends Error {
  constructor() {
    super("Cursor pagination is only supported with sort:recent. Remove the cursor or change the sort order.");
    this.name = "CursorSortError";
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
      country: "tariff_countries",
    },
    // Junction tables replace Postgres array columns.
    // company_hs_codes links tariffs → companies via HS code.
    // tariff_countries links tariffs → country codes.
    customJoins: "LEFT JOIN tariff_hs_codes thc ON thc.tariff_id = tf.id LEFT JOIN companies c ON c.id = thc.company_id",
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

  if (cursor && query.sort !== "recent") {
    throw new CursorSortError();
  }

  const complexity = computeComplexity(query, signalTickers);
  if (complexity > 6) {
    throw new ComplexityError(complexity);
  }

  const params: unknown[] = [];
  const warnings: string[] = [];

  // Check for contradictory date range
  const sinceFilter = query.filters.find((f) => f.key === "since" && !f.negated);
  const beforeFilter = query.filters.find((f) => f.key === "before" && !f.negated);
  if (sinceFilter && beforeFilter) {
    const sinceDate = resolveDate(sinceFilter.values[0]);
    const beforeDate = resolveDate(beforeFilter.values[0]);
    if (sinceDate >= beforeDate) {
      warnings.push(`Date range since:${sinceFilter.values[0]} before:${beforeFilter.values[0]} matches no dates (${sinceDate} >= ${beforeDate}).`);
    }
  }

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

  // Auto-narrow tables based on filter applicability to avoid D1 compound SELECT limits.
  const hasByFilter = query.filters.some((f) => f.key === "by");
  if (hasByFilter) {
    tables = tables.filter((t) => t.hasPerson);
  }
  const hasTickerFilter = query.filters.some((f) => f.key === "ticker");
  if (hasTickerFilter || query.bareText.length > 0) {
    tables = tables.filter((t) => t.hasCompany || !!t.customJoins);
  }

  // D1 has a compound SELECT limit (~500 terms). Each branch generates many terms,
  // so cap at 5 tables. Tables are ordered by importance in the TABLES array.
  if (tables.length > 5) {
    const dropped = tables.slice(5).map((t) => t.eventType).join(", ");
    warnings.push(`Query spans ${tables.length} event types — showing results from the first 5. Excluded: ${dropped}. Add a type: filter for specific results.`);
    tables = tables.slice(0, 5);
  }

  const branches = tables.map((table) => buildBranch(table, query, params, cursor, signalTickers, warnings));

  const innerSql = branches.length === 1 ? branches[0] : branches.join("\nUNION ALL\n");

  params.push(query.limit);

  let sql: string;
  if (query.group) {
    const groupKey = getGroupKey(query.group);
    sql = `SELECT ${groupKey} AS group_key, '${query.group}' AS group_by, COUNT(*) AS count,
      MIN(occurred_at) AS first_seen, MAX(occurred_at) AS last_seen,
      SUM(amount_mid) AS total_amount
    FROM (\n${innerSql}\n) AS base
    GROUP BY ${groupKey}
    ORDER BY count DESC
    LIMIT ?`;
  } else {
    const sortCol = getSortColumn(query.sort);
    sql = `SELECT * FROM (\n${innerSql}\n) AS unified\nORDER BY ${sortCol}\nLIMIT ?`;
  }

  // SQLite does not support DISTINCT ON. Use ROW_NUMBER() window function instead.
  if (needsScoreCTE) {
    sql = `WITH latest_scores AS (
  SELECT company_id, composite_score, market_score, policy_score, insider_score FROM (
    SELECT company_id, composite_score, market_score, policy_score, insider_score,
           ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY computed_at DESC) AS rn
    FROM scores
  ) WHERE rn = 1
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

  joins.push(`LEFT JOIN events e ON e.id = ${table.alias}.event_id`);

  if (table.hasCompany) {
    if (table.name === "lobbying") {
      joins.push(`LEFT JOIN companies c ON c.id = ${table.alias}.client_company_id`);
    } else {
      // LEFT JOIN: some typed records (e.g. sanctions for individuals) have NULL company_id.
      joins.push(`LEFT JOIN companies c ON c.id = ${table.alias}.company_id`);
    }
  } else if (table.customJoins) {
    joins.push(table.customJoins);
  }

  if (table.hasPerson) {
    joins.push(`LEFT JOIN persons p ON p.id = ${table.alias}.person_id`);
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
    const escaped = `%${escapeLike(text)}%`;
    params.push(escaped);
    // SQLite LIKE is case-insensitive for ASCII by default, so ILIKE → LIKE.
    if (table.hasCompany || table.customJoins) {
      clauses.push(`c.ticker LIKE ? ESCAPE '\\'`);
      clauses.push(`c.name LIKE ? ESCAPE '\\'`);
      // Reuse the same param value — push it again for the second placeholder.
      params.push(escaped);
    }
    if (table.hasPerson) {
      clauses.push(`p.name LIKE ? ESCAPE '\\'`);
      params.push(escaped);
    }
    if (clauses.length > 0) {
      wheres.push(`(${clauses.join(" OR ")})`);
    }
  }

  if (cursor) {
    params.push(cursor.occurred_at);
    params.push(cursor.occurred_at);
    params.push(cursor.event_id);
    wheres.push(`(${table.dateCol} < ? OR (${table.dateCol} = ? AND e.id < ?))`);
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
    // SQLite has no ANY($N) — expand to IN (?,?,?).
    const placeholders = filter.values.map(() => "?").join(", ");
    params.push(...filter.values);
    return `${neg}c.ticker IN (${placeholders})`;
  }

  if (filter.key === "sector") {
    if (!table.hasCompany && !table.customJoins) return null;
    params.push(`%${escapeLike(filter.values[0])}%`);
    return `${neg}c.sector LIKE ? ESCAPE '\\'`;
  }

  if (filter.key === "subsector") {
    if (!table.hasCompany) return null;
    params.push(`%${escapeLike(filter.values[0])}%`);
    return `${neg}c.subsector LIKE ? ESCAPE '\\'`;
  }

  if (filter.key === "market-cap") {
    if (!table.hasCompany) return null;
    params.push(filter.values[0]);
    return `${neg}c.market_cap_bucket = ?`;
  }

  if (PERSON_FILTERS.has(filter.key)) {
    if (!table.hasPerson) {
      warnings.push(`${filter.key}: filter not applicable to ${table.eventType}`);
      return "1=0";
    }

    if (filter.key === "by") {
      const isName = filter.values.some((v) => v.includes(" "));
      if (isName) {
        const likeClauses = filter.values.map(() => "p.name LIKE ? ESCAPE '\\'");
        filter.values.forEach((v) => params.push(`%${escapeLike(v)}%`));
        return `${neg}(${likeClauses.join(" OR ")})`;
      }
      const placeholders = filter.values.map(() => "?").join(", ");
      params.push(...filter.values);
      return `${neg}p.slug IN (${placeholders})`;
    }

    if (filter.key === "party") {
      const placeholders = filter.values.map(() => "?").join(", ");
      params.push(...filter.values);
      return `${neg}p.party IN (${placeholders})`;
    }

    if (filter.key === "branch") {
      const roleMap: Record<string, string> = { senate: "senator", house: "representative" };
      const roles = filter.values.map((v) => roleMap[v] || v);
      const placeholders = roles.map(() => "?").join(", ");
      params.push(...roles);
      return `${neg}p.role IN (${placeholders})`;
    }

    if (filter.key === "committee") {
      const placeholders = filter.values.map(() => "?").join(", ");
      params.push(...filter.values);
      return `${neg}EXISTS (SELECT 1 FROM person_committees pc WHERE pc.person_id = p.id AND pc.committee_code IN (${placeholders}))`;
    }
  }

  if (filter.key === "since" || filter.key === "before") {
    if (filter.negated) {
      warnings.push(`Negated ${filter.key}: filter is not supported — ignoring.`);
      return null;
    }
    const resolved = resolveDate(filter.values[0]);
    params.push(resolved);
    const op = filter.key === "since" ? ">=" : "<";
    return `${table.dateCol} ${op} ?`;
  }

  if (filter.key.endsWith("-score") || filter.key === "score") {
    const colMap: Record<string, string> = {
      score: "ls.composite_score",
      "market-score": "ls.market_score",
      "policy-score": "ls.policy_score",
      "insider-score": "ls.insider_score",
    };
    const col = colMap[filter.key];
    if (!col) return null;
    if (!table.hasCompany) {
      warnings.push(`${filter.key}: filter not applicable to ${table.eventType}`);
      return null;
    }
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
    const placeholders = signalTickers.map(() => "?").join(", ");
    params.push(...signalTickers);
    return `${neg}c.ticker IN (${placeholders})`;
  }

  const col = table.filterCols[filter.key];
  if (col) {
    if (filter.key === "country" && table.eventType === "tariff") {
      // SQLite has no array overlap operator (&&).
      // tariff_countries is a junction table: (tariff_id, country).
      const placeholders = filter.values.map(() => "?").join(", ");
      params.push(...filter.values);
      return `${neg}EXISTS (SELECT 1 FROM tariff_countries tc WHERE tc.tariff_id = tf.id AND tc.country IN (${placeholders}))`;
    }
    if (filter.key === "agency" || filter.key === "registrant") {
      params.push(`%${escapeLike(filter.values[0])}%`);
      return `${neg}${col} LIKE ? ESCAPE '\\'`;
    }
    const placeholders = filter.values.map(() => "?").join(", ");
    params.push(...filter.values);
    return `${neg}${col} IN (${placeholders})`;
  }

  return null;
}

function buildProjection(table: TableMeta, needsScores: boolean): string {
  const t = table.alias;
  // SQLite has no typed NULL casts (NULL::text etc.) — plain NULL is correct.
  const person = table.hasPerson
    ? `p.slug AS person_slug, p.name AS person_name, p.party AS person_party, p.branch AS person_branch`
    : `NULL AS person_slug, NULL AS person_name, NULL AS person_party, NULL AS person_branch`;

  const company = table.hasCompany || table.customJoins
    ? `c.ticker, c.name AS company_name, c.sector`
    : `NULL AS ticker, NULL AS company_name, NULL AS sector`;

  const action = table.filterCols["action"]
    ? `${table.filterCols["action"]} AS action`
    : `NULL AS action`;

  const owner = table.eventType === "trade" ? `${t}.owner_type AS owner` : `NULL AS owner`;

  const amountMid = table.amountCol
    ? `(${table.amountCol})${table.amountIsCents ? " / 100" : ""} AS amount_mid`
    : `NULL AS amount_mid`;

  const score = needsScores && table.hasCompany
    ? `ls.composite_score AS score`
    : `NULL AS score`;

  const filedAt = ["trade", "insider", "warn"].includes(table.eventType)
    ? `${t}.filed_at`
    : `NULL AS filed_at`;

  const agency = table.eventType === "contract" ? `${t}.agency` : `NULL AS agency`;
  const program = table.eventType === "sanction" ? `${t}.program` : `NULL AS program`;
  const country = table.eventType === "sanction" ? `${t}.country` : `NULL AS country`;
  const state = table.eventType === "warn" ? `${t}.state` : `NULL AS state`;
  const workers = table.eventType === "warn" ? `${t}.workers_affected` : `NULL AS workers_affected`;
  const entityName = table.eventType === "sanction" ? `${t}.entity_name` : `NULL AS entity_name`;
  const description = table.eventType === "contract" ? `${t}.description` : `NULL AS description`;
  const registrant = table.eventType === "lobbying" ? `${t}.registrant` : `NULL AS registrant`;
  const filingType = table.eventType === "court" ? `${t}.filing_type` : `NULL AS filing_type`;
  const filerName = table.eventType === "insider" ? `${t}.filer_name` : `NULL AS filer_name`;
  const filerTitle = table.eventType === "insider" ? `${t}.filer_title` : `NULL AS filer_title`;
  const shares = table.eventType === "insider" ? `${t}.shares` : `NULL AS shares`;

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
    // SQLite has no date_trunc() — use strftime/date equivalents.
    case "week": return "date(occurred_at, 'weekday 1', '-7 days')";
    case "month": return "date(occurred_at, 'start of month')";
    case "company": return "ticker";
    case "person": return "person_slug";
    case "sector": return "sector";
    case "type": return "event_type";
    default: throw new Error(`Invalid group key: ${group}`);
  }
}

function buildRangeClause(col: string, filter: Filter, params: unknown[]): string | null {
  if (filter.operator === "..") {
    const lo = Number(filter.values[0]);
    const hi = Number(filter.upperBound!);
    if (isNaN(lo) || isNaN(hi)) return null;
    params.push(lo);
    params.push(hi);
    return `${col} BETWEEN ? AND ?`;
  }
  const SAFE_OPS = new Set([">", "<", ">=", "<="]);
  const op = filter.operator && SAFE_OPS.has(filter.operator) ? filter.operator : ">=";
  const num = Number(filter.values[0]);
  if (isNaN(num)) return null;
  params.push(num);
  return `${col} ${op} ?`;
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
  const hasBareText = query.bareText.length > 0;
  if (!hasType && !hasTicker && !hasPerson && !hasDate && !hasBareText) score += 2;

  return score;
}

function escapeLike(v: string): string {
  return v.replace(/[%_]/g, (c) => `\\${c}`);
}

import type { Filter, ParsedQuery } from "./types";

const VALID_KEYS = new Set([
  "type", "ticker", "sector", "subsector", "market-cap",
  "by", "party", "branch", "committee",
  "action", "owner", "agency", "country", "program",
  "state", "registrant", "filing", "signal",
  "score", "market-score", "policy-score", "insider-score",
  "amount", "workers",
  "since", "before",
  "sort", "group", "limit",
]);

const VALID_SORTS = new Set(["recent", "score", "amount", "score-delta"]);
const VALID_GROUPS = new Set(["type", "company", "person", "sector", "week", "month"]);

const NUMERIC_KEYS = new Set([
  "score", "market-score", "policy-score", "insider-score",
  "amount", "workers", "limit",
]);

const MODIFIER_KEYS = new Set(["sort", "group", "limit"]);

const SINGLE_VALUE_KEYS = new Set(["since", "before", "sector", "subsector"]);

const MAX_CLAUSES = 20;
const MAX_VALUES_PER_KEY = 20;
const MAX_LIMIT = 200;
const DEFAULT_LIMIT = 50;

export class ParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ParseError";
  }
}

export function parse(input: string): ParsedQuery {
  const trimmed = input.trim();
  if (!trimmed) {
    return { filters: [], bareText: [], sort: "recent", group: null, limit: DEFAULT_LIMIT };
  }

  const tokens = tokenize(trimmed);

  if (tokens.length > MAX_CLAUSES) {
    throw new ParseError(`Too many clauses (max ${MAX_CLAUSES})`);
  }

  const filters: Filter[] = [];
  const bareText: string[] = [];
  let sort = "recent";
  let group: string | null = null;
  let limit = DEFAULT_LIMIT;

  for (const token of tokens) {
    const negated = token.startsWith("-") && token.includes(":");
    const raw = negated ? token.slice(1) : token;
    const colonIdx = raw.indexOf(":");

    if (colonIdx === -1) {
      bareText.push(token.startsWith("-") ? token.slice(1) : token);
      continue;
    }

    const key = raw.slice(0, colonIdx).toLowerCase();
    const valueStr = raw.slice(colonIdx + 1);

    if (!VALID_KEYS.has(key)) {
      throw new ParseError(`Unknown filter key: "${key}"`);
    }

    if (MODIFIER_KEYS.has(key) && negated) {
      throw new ParseError(`Cannot negate modifier "${key}"`);
    }

    if (key === "sort") {
      if (!VALID_SORTS.has(valueStr)) {
        throw new ParseError(`Invalid sort value: "${valueStr}". Valid: ${[...VALID_SORTS].join(", ")}`);
      }
      sort = valueStr;
      continue;
    }
    if (key === "group") {
      if (!VALID_GROUPS.has(valueStr)) {
        throw new ParseError(`Invalid group value: "${valueStr}". Valid: ${[...VALID_GROUPS].join(", ")}`);
      }
      group = valueStr;
      continue;
    }
    if (key === "limit") {
      const n = parseInt(valueStr, 10);
      if (isNaN(n) || n < 1) {
        throw new ParseError(`Invalid limit: "${valueStr}"`);
      }
      limit = Math.min(n, MAX_LIMIT);
      continue;
    }

    const filter = parseValue(key, valueStr, negated);
    if (filter.values.length > MAX_VALUES_PER_KEY) {
      throw new ParseError(`Too many values for "${key}" (max ${MAX_VALUES_PER_KEY})`);
    }
    if (SINGLE_VALUE_KEYS.has(key) && filter.values.length > 1) {
      throw new ParseError(`"${key}" accepts only one value`);
    }
    filters.push(filter);
  }

  return { filters, bareText, sort, group, limit };
}

function tokenize(input: string): string[] {
  const tokens: string[] = [];
  let current = "";
  let inQuote = false;

  for (let i = 0; i < input.length; i++) {
    const ch = input[i];
    if (ch === '"') {
      inQuote = !inQuote;
      current += ch;
    } else if (ch === " " && !inQuote) {
      if (current) {
        tokens.push(current);
        current = "";
      }
    } else {
      current += ch;
    }
  }

  if (inQuote) {
    throw new ParseError("Unterminated quoted string");
  }

  if (current) tokens.push(current);
  return tokens;
}

function parseValue(key: string, valueStr: string, negated: boolean): Filter {
  const rangeMatch = valueStr.match(/^(>=?|<=?)(.*)$/);
  if (rangeMatch && NUMERIC_KEYS.has(key)) {
    if (!rangeMatch[2]) {
      throw new ParseError(`Empty value after operator in "${key}:${valueStr}"`);
    }
    return {
      key,
      values: [rangeMatch[2]],
      negated,
      operator: rangeMatch[1] as Filter["operator"],
    };
  }

  const dotDotMatch = valueStr.match(/^([^.]+)\.\.([^.]+)$/);
  if (dotDotMatch && NUMERIC_KEYS.has(key)) {
    return {
      key,
      values: [dotDotMatch[1]],
      negated,
      operator: "..",
      upperBound: dotDotMatch[2],
    };
  }

  if (valueStr.startsWith('"') && valueStr.endsWith('"')) {
    return {
      key,
      values: [valueStr.slice(1, -1)],
      negated,
    };
  }

  const values = valueStr.split(",").map(v => v.trim()).filter(Boolean);
  if (values.length === 0) {
    throw new ParseError(`Empty value for filter "${key}"`);
  }
  return { key, values, negated };
}

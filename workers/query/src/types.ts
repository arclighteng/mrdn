/** A single parsed filter clause from the MQL query string. */
export interface Filter {
  key: string;
  values: string[];
  negated: boolean;
  operator?: ">" | "<" | ">=" | "<=" | "..";
  /** For range expressions like 50..80, the second bound. */
  upperBound?: string;
}

/** The output of the MQL parser. */
export interface ParsedQuery {
  filters: Filter[];
  bareText: string[];
  sort: string;
  group: string | null;
  limit: number;
}

/** Compiled SQL ready for execution. */
export interface CompiledQuery {
  sql: string;
  params: unknown[];
  complexity: number;
  warnings: string[];
}

/** Query result envelope. */
export interface QueryResponse {
  results: Record<string, unknown>[];
  meta: {
    query: string;
    query_ms: number;
    result_count: number;
    has_more: boolean;
    next_cursor: string | null;
    data_as_of: string | null;
    grouped: boolean;
  };
  warnings: string[];
}

/** Error response shape. */
export interface ErrorResponse {
  error: string;
  message: string;
  code: string;
}

/** Cloudflare Worker environment bindings. */
export interface Env {
  DB: D1Database;
  MQL_KV: KVNamespace;
}

/** Cursor for pagination. */
export interface Cursor {
  occurred_at: string;
  event_id: number;
  data_as_of: string;
}

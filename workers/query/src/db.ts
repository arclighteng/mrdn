import { neon } from "@neondatabase/serverless";

export interface QueryResult {
  rows: Record<string, unknown>[];
  duration_ms: number;
}

/**
 * Execute a parameterized SQL query against Neon Postgres.
 * Uses the HTTP-based neon() driver (stateless, no WebSocket needed).
 * statement_timeout is enforced via the connection string parameter.
 *
 * IMPORTANT: DATABASE_URL must include `?options=-c%20statement_timeout%3D8000`
 * (or the secret should be set with this suffix). This sets an 8-second
 * statement timeout at the connection level, which works with the HTTP driver
 * without needing transactions or WebSocket mode.
 */
export async function executeQuery(
  databaseUrl: string,
  sql: string,
  params: unknown[],
): Promise<QueryResult> {
  // Ensure statement_timeout is set in the connection string
  const url = databaseUrl.includes("statement_timeout")
    ? databaseUrl
    : databaseUrl + (databaseUrl.includes("?") ? "&" : "?") + "options=-c%20statement_timeout%3D8000";

  const sqlClient = neon(url);
  const start = Date.now();
  const rows = await sqlClient(sql, params);
  return {
    rows: rows as Record<string, unknown>[],
    duration_ms: Date.now() - start,
  };
}

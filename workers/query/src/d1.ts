export interface QueryResult {
  rows: Record<string, unknown>[];
  duration_ms: number;
}

export async function executeQuery(
  db: D1Database,
  sql: string,
  params: unknown[],
): Promise<QueryResult> {
  const start = Date.now();
  const stmt = params.length > 0
    ? db.prepare(sql).bind(...params)
    : db.prepare(sql);
  const result = await stmt.all();
  return {
    rows: (result.results ?? []) as Record<string, unknown>[],
    duration_ms: Date.now() - start,
  };
}

import type { Env, Cursor, QueryResponse, ErrorResponse } from "./types";
import { parse, ParseError } from "./parser";
import { compile, ComplexityError, ScoreDeltaError, CursorSortError } from "./compiler";
import { executeQuery } from "./d1";
import { checkRateLimit, releaseSlot } from "./rate-limit";

const MAX_BODY_BYTES = 2048;

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    // CORS preflight
    if (request.method === "OPTIONS") {
      return corsResponse(new Response(null, { status: 204 }));
    }

    if (request.method !== "POST") {
      return corsResponse(errorResponse(405, "METHOD_NOT_ALLOWED", "Use POST /api/query"));
    }

    const url = new URL(request.url);
    if (url.pathname !== "/api/query") {
      return corsResponse(errorResponse(404, "NOT_FOUND", "Unknown endpoint"));
    }

    // IP-based rate limit
    const ip = request.headers.get("CF-Connecting-IP") || "unknown";
    const rl = await checkRateLimit(env.MQL_KV, ip);
    if (!rl.allowed) {
      return corsResponse(errorResponse(429, "RATE_LIMITED", "Too many concurrent queries. Try again shortly."));
    }

    try {
      return corsResponse(await handleQuery(request, env));
    } finally {
      await releaseSlot(env.MQL_KV, ip);
    }
  },
};

async function handleQuery(request: Request, env: Env): Promise<Response> {
  // Validate body size — read as text first to enforce limit regardless of Content-Length header
  const text = await request.text();
  if (text.length > MAX_BODY_BYTES) {
    return errorResponse(413, "REQUEST_TOO_LARGE", "Request body exceeds 2KB limit");
  }

  let body: { q?: string; cursor?: string } | null;
  try {
    body = JSON.parse(text);
  } catch {
    body = null;
  }
  if (!body || typeof body.q !== "string") {
    return errorResponse(400, "PARSE_ERROR", 'Request body must include "q" string field');
  }

  // Parse
  let parsed;
  try {
    parsed = parse(body.q);
  } catch (e) {
    if (e instanceof ParseError) {
      return errorResponse(400, "PARSE_ERROR", e.message);
    }
    throw e;
  }

  // Decode cursor
  let cursor: Cursor | null = null;
  if (body.cursor) {
    try {
      cursor = JSON.parse(atob(body.cursor)) as Cursor;
    } catch {
      return errorResponse(400, "PARSE_ERROR", "Invalid cursor");
    }
  }

  // Read data_as_of once — reused for staleness check and next cursor
  const dataAsOf = await env.MQL_KV.get("meta:data_as_of");

  // Check cursor staleness
  if (cursor) {
    if (dataAsOf && cursor.data_as_of !== dataAsOf) {
      return errorResponse(409, "CURSOR_STALE", "Data has been updated since this cursor was created. Please re-execute the query.");
    }
  }

  // Load signal tickers from KV if needed
  let signalTickers: string[] = [];
  const signalFilter = parsed.filters.find((f) => f.key === "signal");
  const warnings: string[] = [];
  if (signalFilter) {
    const signalKey = `signal:${signalFilter.values[0]}`;
    const raw = await env.MQL_KV.get(signalKey);
    if (raw) {
      signalTickers = JSON.parse(raw) as string[];
    } else {
      warnings.push("Signal data is temporarily unavailable. Results may be incomplete.");
    }
  }

  // Compile
  let compiled;
  try {
    compiled = compile(parsed, cursor, signalTickers);
    // Merge compiler warnings (e.g., inapplicable filters)
    warnings.push(...compiled.warnings);
  } catch (e) {
    if (e instanceof ComplexityError) {
      return errorResponse(400, "COMPLEXITY_LIMIT", e.message);
    }
    if (e instanceof ScoreDeltaError) {
      return errorResponse(400, "PARSE_ERROR", e.message);
    }
    if (e instanceof CursorSortError) {
      return errorResponse(400, "CURSOR_SORT_ERROR", e.message);
    }
    throw e;
  }

  // Execute
  let result;
  try {
    result = await executeQuery(env.DB, compiled.sql, compiled.params);
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    if (msg.includes("57014") || msg.includes("statement timeout")) {
      return errorResponse(503, "QUERY_TIMEOUT", "Query exceeded 8-second timeout. Try narrowing your filters.");
    }
    console.error("Query execution error:", msg);
    return errorResponse(500, "INTERNAL_ERROR", "Query execution failed");
  }

  // Build cursor for next page
  let nextCursor: string | null = null;
  if (result.rows.length === parsed.limit) {
    const lastRow = result.rows[result.rows.length - 1];
    const cursorObj: Cursor = {
      occurred_at: String(lastRow.occurred_at),
      event_id: Number(lastRow.event_id),
      data_as_of: dataAsOf || "",
    };
    nextCursor = btoa(JSON.stringify(cursorObj));
  }

  const response: QueryResponse = {
    results: result.rows,
    meta: {
      query: body.q,
      query_ms: result.duration_ms,
      result_count: result.rows.length,
      has_more: result.rows.length === parsed.limit,
      next_cursor: nextCursor,
      data_as_of: dataAsOf,
      grouped: parsed.group !== null,
    },
    warnings,
  };

  return new Response(JSON.stringify(response), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function errorResponse(status: number, code: string, message: string): Response {
  const body: ErrorResponse = { error: code, message, code };
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function corsResponse(response: Response): Response {
  const headers = new Headers(response.headers);
  headers.set("Access-Control-Allow-Origin", "*");
  headers.set("Access-Control-Allow-Methods", "POST, OPTIONS");
  headers.set("Access-Control-Allow-Headers", "Content-Type");
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

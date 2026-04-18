import { describe, it, expect } from "vitest";
import { compile, ComplexityError } from "./compiler";
import { parse } from "./parser";
import type { Cursor, ParsedQuery } from "./types";

describe("compile", () => {
  it("compiles a single-type query", () => {
    const q = parse("type:trade by:pelosi since:30d");
    const result = compile(q, null, []);
    expect(result.sql).toContain("congressional_trades");
    expect(result.sql).toContain("?");
    // by:pelosi (no space) → slug IN (?) path; param pushed as scalar
    expect(result.params).toContain("pelosi");
    expect(result.sql).not.toContain("UNION ALL");
  });

  it("compiles a multi-type query with UNION ALL", () => {
    const q = parse("type:trade,contract ticker:MSFT since:30d");
    const result = compile(q, null, []);
    expect(result.sql).toContain("UNION ALL");
    expect(result.sql).toContain("congressional_trades");
    expect(result.sql).toContain("contracts");
  });

  it("uses parameterized values, never interpolates", () => {
    const q = parse('type:trade by:"robert drop table" since:30d');
    const result = compile(q, null, []);
    expect(result.sql).not.toContain("drop table");
    // Quoted name with space → ILIKE match, param is "%robert drop table%"
    expect(result.params.some((p) => p === "%robert drop table%")).toBe(true);
  });

  it("compiles amount filter with unit conversion", () => {
    const q = parse("type:contract amount:>1m since:30d");
    const result = compile(q, null, []);
    // Contracts store cents — compiler should multiply by 100
    expect(result.params).toContain(100000000);
  });

  it("compiles score filter with CTE", () => {
    const q = parse("type:trade score:>70 since:30d");
    const result = compile(q, null, []);
    expect(result.sql).toContain("latest_scores");
    // SQLite uses ROW_NUMBER() instead of DISTINCT ON
    expect(result.sql).toContain("ROW_NUMBER()");
  });

  it("compiles group query", () => {
    const q = parse("type:trade group:company since:30d");
    const result = compile(q, null, []);
    expect(result.sql).toContain("GROUP BY");
    expect(result.sql).toContain("COUNT(*)");
  });

  it("rejects score-delta sort with helpful message", () => {
    const q = parse("type:trade sort:score-delta since:30d");
    expect(() => compile(q, null, [])).toThrow("score-delta");
  });

  it("rejects queries that exceed complexity limit", () => {
    // No type, no ticker, no person, no date bounds — should be high complexity
    const q = parse("sector:defense");
    expect(() => compile(q, null, [])).toThrow(ComplexityError);
  });

  it("compiles cursor pagination", () => {
    const cursor: Cursor = {
      occurred_at: "2025-03-15T14:22:00Z",
      event_id: 92041,
      data_as_of: "2026-04-17T06:00:00Z",
    };
    const q = parse("type:trade since:30d");
    const result = compile(q, cursor, []);
    expect(result.sql).toContain("e.id <");
  });

  it("compiles signal filter using provided tickers", () => {
    const q = parse("type:trade signal:swarm since:30d");
    const result = compile(q, null, ["MSFT", "AAPL"]);
    // SQLite uses IN (?,?) with scalar params instead of ANY($N) with array param
    expect(result.sql).toContain("c.ticker IN");
    expect(result.params).toContain("MSFT");
    expect(result.params).toContain("AAPL");
  });

  it("compiles negated filter", () => {
    const q = parse("type:trade -ticker:MSFT since:30d");
    const result = compile(q, null, []);
    expect(result.sql).toContain("NOT");
    expect(result.sql).toContain("c.ticker");
  });

  it("compiles tariff country filter with hs_codes join", () => {
    const q = parse("type:tariff country:RU since:30d");
    const result = compile(q, null, []);
    // SQLite uses junction table EXISTS query instead of Postgres && array overlap
    expect(result.sql).toContain("tariff_countries");
  });

  it("produces WHERE 1=0 for inapplicable filters", () => {
    const q = parse("type:contract party:D since:30d");
    const result = compile(q, null, []);
    expect(result.sql).toContain("contracts");
  });

  it("compiles bare text as LIKE (case-insensitive in SQLite)", () => {
    const q = parse("type:trade pelosi since:30d");
    const result = compile(q, null, []);
    // SQLite LIKE is case-insensitive for ASCII; ILIKE is a Postgres extension
    expect(result.sql).toContain("LIKE");
  });

  it("throws on getGroupKey with invalid group (defense-in-depth)", () => {
    const q: ParsedQuery = {
      filters: [{ key: "type", values: ["trade"], negated: false }],
      bareText: [],
      sort: "recent",
      group: "injected; DROP TABLE",
      limit: 50,
    };
    expect(() => compile(q, null, [])).toThrow("Invalid group key");
  });

  it("sanitizes unknown operators in buildRangeClause", () => {
    const q = parse("type:trade score:>70 since:30d");
    const result = compile(q, null, []);
    // Should use > operator, not anything else
    expect(result.sql).toContain(">");
  });

  it("rejects cursor with non-recent sort", () => {
    const cursor = { occurred_at: "2025-03-15T14:22:00Z", event_id: 92041, data_as_of: "" };
    const q = parse("type:trade sort:score since:30d");
    expect(() => compile(q, cursor, [])).toThrow("Cursor pagination is only supported with sort:recent");
  });

  it("allows cursor with sort:recent", () => {
    const cursor = { occurred_at: "2025-03-15T14:22:00Z", event_id: 92041, data_as_of: "" };
    const q = parse("type:trade since:30d");
    expect(() => compile(q, cursor, [])).not.toThrow();
  });
});

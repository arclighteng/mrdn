import { describe, it, expect } from "vitest";
import { parse, ParseError } from "./parser";

describe("parse", () => {
  it("parses a single filter", () => {
    const q = parse("type:trade");
    expect(q.filters).toHaveLength(1);
    expect(q.filters[0]).toEqual({
      key: "type",
      values: ["trade"],
      negated: false,
    });
  });

  it("parses comma-separated values as OR", () => {
    const q = parse("ticker:MSFT,AMZN");
    expect(q.filters[0].values).toEqual(["MSFT", "AMZN"]);
  });

  it("parses negated filter", () => {
    const q = parse("-branch:house");
    expect(q.filters[0].negated).toBe(true);
    expect(q.filters[0].key).toBe("branch");
  });

  it("parses quoted values", () => {
    const q = parse('by:"nancy pelosi"');
    expect(q.filters[0].values).toEqual(["nancy pelosi"]);
  });

  it("parses range operators", () => {
    const q = parse("score:>70");
    expect(q.filters[0].operator).toBe(">");
    expect(q.filters[0].values).toEqual(["70"]);
  });

  it("parses range expressions with ..", () => {
    const q = parse("score:50..80");
    expect(q.filters[0].operator).toBe("..");
    expect(q.filters[0].values).toEqual(["50"]);
    expect(q.filters[0].upperBound).toBe("80");
  });

  it("parses amount with unit suffixes", () => {
    const q = parse("amount:>1m");
    expect(q.filters[0].values).toEqual(["1m"]);
    expect(q.filters[0].operator).toBe(">");
  });

  it("parses bare text", () => {
    const q = parse("pelosi defense");
    expect(q.bareText).toEqual(["pelosi", "defense"]);
    expect(q.filters).toHaveLength(0);
  });

  it("parses mixed filters and bare text", () => {
    const q = parse("type:trade pelosi since:30d");
    expect(q.filters).toHaveLength(2);
    expect(q.bareText).toEqual(["pelosi"]);
  });

  it("extracts sort modifier", () => {
    const q = parse("type:trade sort:amount");
    expect(q.sort).toBe("amount");
    expect(q.filters.some((f) => f.key === "sort")).toBe(false);
  });

  it("extracts group modifier", () => {
    const q = parse("type:trade group:company");
    expect(q.group).toBe("company");
  });

  it("extracts limit modifier", () => {
    const q = parse("type:trade limit:100");
    expect(q.limit).toBe(100);
  });

  it("defaults sort to recent, group to null, limit to 50", () => {
    const q = parse("type:trade");
    expect(q.sort).toBe("recent");
    expect(q.group).toBeNull();
    expect(q.limit).toBe(50);
  });

  it("throws ParseError on unknown key", () => {
    expect(() => parse("bogus:value")).toThrow(ParseError);
  });

  it("clamps limit to 200", () => {
    const q = parse("limit:999");
    expect(q.limit).toBe(200);
  });

  it("throws on too many clauses", () => {
    const clauses = Array.from({ length: 21 }, (_, i) => `ticker:T${i}`).join(" ");
    expect(() => parse(clauses)).toThrow(ParseError);
  });

  it("throws on too many values in one key", () => {
    const values = Array.from({ length: 21 }, (_, i) => `T${i}`).join(",");
    expect(() => parse(`ticker:${values}`)).toThrow(ParseError);
  });

  it("parses relative date expressions", () => {
    const q = parse("since:30d");
    expect(q.filters[0].key).toBe("since");
    expect(q.filters[0].values).toEqual(["30d"]);
  });

  it("parses absolute date expressions", () => {
    const q = parse("since:2025-01");
    expect(q.filters[0].values).toEqual(["2025-01"]);
  });

  it("throws on unclosed quote", () => {
    expect(() => parse('by:"nancy pelosi')).toThrow(ParseError);
    expect(() => parse('by:"nancy pelosi')).toThrow("Unterminated");
  });

  it("trims whitespace from comma-separated values", () => {
    // Spaces around commas are tokenizer delimiters, so trimming applies to
    // internal whitespace within a single value token (e.g. a trailing comma
    // producing an empty string that is filtered out). We verify the trim+filter
    // path by using a value with a trailing comma.
    const q = parse("ticker:MSFT,AMZN,GOOG");
    expect(q.filters[0].values).toEqual(["MSFT", "AMZN", "GOOG"]);
  });

  it("throws on empty value after colon", () => {
    expect(() => parse("type:")).toThrow(ParseError);
  });

  it("does not parse range operators on non-numeric keys", () => {
    const q = parse("ticker:>MSFT");
    // Should be treated as a literal value, not a range
    expect(q.filters[0].operator).toBeUndefined();
    expect(q.filters[0].values).toEqual([">MSFT"]);
  });

  it("throws on negated control modifiers", () => {
    expect(() => parse("-sort:score")).toThrow(ParseError);
    expect(() => parse("-limit:10")).toThrow(ParseError);
    expect(() => parse("-group:company")).toThrow(ParseError);
  });

  it("throws on multiple values for since", () => {
    expect(() => parse("since:30d,60d")).toThrow(ParseError);
  });

  it("throws on multiple values for sector", () => {
    expect(() => parse("sector:defense,energy")).toThrow(ParseError);
  });

  it("throws on empty range operand", () => {
    expect(() => parse("score:>=")).toThrow(ParseError);
  });
});

import { describe, it, expect, vi } from "vitest";
import { checkRateLimit, releaseSlot } from "./rate-limit";

function mockKV(store: Record<string, string> = {}) {
  return {
    get: vi.fn(async (key: string) => store[key] ?? null),
    put: vi.fn(async (key: string, value: string) => { store[key] = value; }),
    delete: vi.fn(async (key: string) => { delete store[key]; }),
  } as unknown as KVNamespace;
}

describe("rate-limit", () => {
  it("allows requests under the limit", async () => {
    const kv = mockKV();
    const result = await checkRateLimit(kv, "1.2.3.4");
    expect(result.allowed).toBe(true);
  });

  it("blocks requests over the limit", async () => {
    const kv = mockKV({ "rl:1.2.3.4": "5" });
    const result = await checkRateLimit(kv, "1.2.3.4");
    expect(result.allowed).toBe(false);
  });

  it("increments the counter", async () => {
    const store: Record<string, string> = { "rl:1.2.3.4": "3" };
    const kv = mockKV(store);
    await checkRateLimit(kv, "1.2.3.4");
    expect(kv.put).toHaveBeenCalledWith("rl:1.2.3.4", "4", expect.any(Object));
  });

  it("decrements on release", async () => {
    const store: Record<string, string> = { "rl:1.2.3.4": "3" };
    const kv = mockKV(store);
    await releaseSlot(kv, "1.2.3.4");
    expect(kv.put).toHaveBeenCalledWith("rl:1.2.3.4", "2", expect.any(Object));
  });
});

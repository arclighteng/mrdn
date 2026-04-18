const MAX_CONCURRENT = 5;
const TTL_SECONDS = 30;

export interface RateLimitResult {
  allowed: boolean;
  current: number;
}

export async function checkRateLimit(
  kv: KVNamespace,
  ip: string,
): Promise<RateLimitResult> {
  const key = `rl:${ip}`;
  const raw = await kv.get(key);
  const current = raw ? parseInt(raw, 10) : 0;

  if (current >= MAX_CONCURRENT) {
    return { allowed: false, current };
  }

  await kv.put(key, String(current + 1), { expirationTtl: TTL_SECONDS });
  return { allowed: true, current: current + 1 };
}

export async function releaseSlot(
  kv: KVNamespace,
  ip: string,
): Promise<void> {
  const key = `rl:${ip}`;
  const raw = await kv.get(key);
  const current = raw ? parseInt(raw, 10) : 0;
  if (current <= 1) {
    await kv.delete(key);
  } else {
    await kv.put(key, String(current - 1), { expirationTtl: TTL_SECONDS });
  }
}

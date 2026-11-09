import { Inject, Injectable, Logger } from '@nestjs/common';
import type Redis from 'ioredis';
import { randomUUID } from 'crypto';
import { REDIS_CLIENT } from '../redis/redis.module';

export interface RateLimitResult {
  allowed: boolean;
  remaining: number;
  resetAtMs: number;
}

/**
 * Atomic sliding-window rate limiter backed by Redis.
 *
 * Valid Lua only (no JavaScript-isms). The script:
 *  1. Evicts entries older than the window (ZREMRANGEBYSCORE).
 *  2. Counts remaining entries (ZCARD).
 *  3. Denies when count >= limit, returning when the oldest entry expires.
 *  4. Otherwise records this hit with a unique member and sets key expiry.
 *
 * If Redis is unreachable, the limiter fails OPEN (allows the request) and
 * logs a warning. Rate limiting is defense-in-depth; authentication
 * correctness never depends on Redis availability. This tradeoff is
 * documented in the README and security plan.
 */
const SLIDING_WINDOW_LUA = `
local key = KEYS[1]
local nowMs = tonumber(ARGV[1])
local windowStartMs = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local ttlSec = tonumber(ARGV[4])
local member = ARGV[5]

redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStartMs)
local count = redis.call('ZCARD', key)
if count >= limit then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local resetAtMs = nowMs + (ttlSec * 1000)
  if #oldest >= 2 then
    resetAtMs = tonumber(oldest[2]) + (ttlSec * 1000)
  end
  return {0, 0, resetAtMs}
end
redis.call('ZADD', key, nowMs, member)
redis.call('EXPIRE', key, ttlSec)
local remaining = limit - count - 1
if remaining < 0 then
  remaining = 0
end
return {1, remaining, nowMs + (ttlSec * 1000)}
`;

@Injectable()
export class RateLimitService {
  private readonly logger = new Logger(RateLimitService.name);

  constructor(@Inject(REDIS_CLIENT) private readonly redis: Redis) {}

  async hit(key: string, limit: number, windowSec: number): Promise<RateLimitResult> {
    const nowMs = Date.now();
    const windowStartMs = nowMs - windowSec * 1000;
    const member = `${nowMs}:${randomUUID()}`;
    try {
      const res = (await this.redis.eval(
        SLIDING_WINDOW_LUA,
        1,
        key,
        String(nowMs),
        String(windowStartMs),
        String(limit),
        String(windowSec),
        member,
      )) as [number, number, number];
      return {
        allowed: res[0] === 1,
        remaining: Number(res[1]),
        resetAtMs: Number(res[2]),
      };
    } catch (err) {
      this.logger.warn(
        `Rate-limit Redis error for key "${key}" - failing open: ${(err as Error).message}`,
      );
      return { allowed: true, remaining: limit, resetAtMs: nowMs + windowSec * 1000 };
    }
  }
}

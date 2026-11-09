import { SetMetadata } from '@nestjs/common';

export interface RateLimitOptions {
  /** Max attempts within the window. */
  limit: number;
  /** Window length in seconds. */
  windowSec: number;
  /** Stable key fragment, e.g. 'register'. Full key: rl:<key>:<ip|user:...>. */
  key: string;
  /**
   * 'ip' (default, preserves legacy behavior) or 'user' (per authenticated
   * user id, falling back to IP when no identity is present). Additive:
   * existing routes without this field are unaffected.
   */
  scope?: 'ip' | 'user';
}

export const RATE_LIMIT_KEY = 'campus_hive_rate_limit';

/** Attach per-route rate-limit configuration consumed by RateLimitGuard. */
export const RateLimit = (options: RateLimitOptions) =>
  SetMetadata(RATE_LIMIT_KEY, options);

import {
  CanActivate,
  ExecutionContext,
  HttpException,
  HttpStatus,
  Injectable,
} from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { Request, Response } from 'express';
import { RateLimitService } from './rate-limit.service';
import {
  RATE_LIMIT_KEY,
  RateLimitOptions,
} from './rate-limit.decorator';
import { ApiErrorCode } from '../types/api-response';

/**
 * Enforces @RateLimit metadata. Keyed per client IP + route fragment.
 * On denial responds 429 with the RATE_LIMITED error code and
 * standard X-RateLimit headers. No-ops on routes without metadata.
 */
@Injectable()
export class RateLimitGuard implements CanActivate {
  constructor(
    private readonly reflector: Reflector,
    private readonly rateLimit: RateLimitService,
  ) {}

  async canActivate(context: ExecutionContext): Promise<boolean> {
    const options = this.reflector.get<RateLimitOptions | undefined>(
      RATE_LIMIT_KEY,
      context.getHandler(),
    );
    if (!options) return true;

    const req = context.switchToHttp().getRequest<Request>();
    const res = context.switchToHttp().getResponse<Response>();
    const key = `rl:${options.key}:${this.scopeKey(req, options)}`;

    const result = await this.rateLimit.hit(key, options.limit, options.windowSec);
    res.setHeader('X-RateLimit-Limit', String(options.limit));
    res.setHeader('X-RateLimit-Remaining', String(result.remaining));
    res.setHeader('X-RateLimit-Reset', String(Math.ceil(result.resetAtMs / 1000)));

    if (!result.allowed) {
      throw new HttpException(
        {
          code: ApiErrorCode.RATE_LIMITED,
          message: 'Too many requests. Please try again later.',
        },
        HttpStatus.TOO_MANY_REQUESTS,
      );
    }
    return true;
  }

  private scopeKey(req: Request, options: RateLimitOptions): string {
    if (options.scope === 'user') {
      const sub = (req as unknown as { user?: { sub?: unknown } }).user?.sub;
      if (typeof sub === 'string' && sub.length > 0) return `user:${sub}`;
    }
    return this.clientIp(req);
  }

  private clientIp(req: Request): string {
    // Express resolves req.ip using the application's 'trust proxy' setting,
    // so the limiter and the application derive client IP consistently.
    // We deliberately never parse X-Forwarded-For ourselves: when the proxy
    // chain is untrusted the header is attacker-controlled and must be
    // ignored; when TRUST_PROXY is configured, Express applies it.
    return req.ip ?? req.socket?.remoteAddress ?? 'unknown';
  }
}

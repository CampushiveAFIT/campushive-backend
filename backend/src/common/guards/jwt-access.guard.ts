import {
  CanActivate,
  ExecutionContext,
  Injectable,
  UnauthorizedException,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import { Request } from 'express';
import { ApiErrorCode } from '../types/api-response';

export interface AccessTokenPayload {
  sub: string;
  role: string;
  jti: string;
  iat?: number;
  exp?: number;
}

/** Validates the Bearer access JWT and attaches the payload as req.user. */
@Injectable()
export class JwtAccessGuard implements CanActivate {
  constructor(
    private readonly jwt: JwtService,
    private readonly config: ConfigService,
  ) {}

  async canActivate(context: ExecutionContext): Promise<boolean> {
    const req = context.switchToHttp().getRequest<Request>();
    const token = this.extractBearer(req);
    if (!token) {
      throw new UnauthorizedException({
        code: ApiErrorCode.INVALID_CREDENTIALS,
        message: 'Authentication required.',
      });
    }
    try {
      const payload = await this.jwt.verifyAsync<AccessTokenPayload>(token, {
        secret: this.config.get<string>('jwt.accessSecret'),
      });
      if (!payload?.sub || !payload?.jti) {
        throw new Error('malformed payload');
      }
      (req as unknown as { user: AccessTokenPayload }).user = payload;
      return true;
    } catch {
      throw new UnauthorizedException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Invalid or expired access token.',
      });
    }
  }

  private extractBearer(req: Request): string | null {
    const header = req.headers.authorization;
    if (!header) return null;
    const [scheme, token] = header.split(' ');
    if (scheme?.toLowerCase() !== 'bearer' || !token) return null;
    return token;
  }
}

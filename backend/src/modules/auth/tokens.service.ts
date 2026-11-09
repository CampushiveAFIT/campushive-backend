import { Injectable } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import { randomUUID } from 'crypto';
import type { Role, User } from '@prisma/client';
import {
  randomOpaqueToken,
  sha256Hex,
} from '../../common/crypto/token-hash.util';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';

export interface IssuedTokens {
  accessToken: string;
  refreshToken: string;
  refreshExpiresAt: Date;
}

/**
 * Token issuance and digest helpers.
 *
 * - Access token: short-lived signed JWT { sub, role, jti }.
 * - Refresh token: opaque cryptographically random string. Only its
 *   SHA-256 digest is ever persisted or used for lookup.
 */
@Injectable()
export class TokensService {
  constructor(
    private readonly jwt: JwtService,
    private readonly config: ConfigService,
  ) {}

  signAccessToken(user: Pick<User, 'id' | 'role'>): string {
    const payload: AccessTokenPayload = {
      sub: user.id,
      role: user.role as Role as string,
      jti: randomUUID(),
    };
    return this.jwt.sign(payload, {
      secret: this.config.get<string>('jwt.accessSecret'),
      expiresIn: this.config.get<number>('jwt.accessTtlSec', 900),
    });
  }

  /** Returns { raw, digest, expiresAt } for a new opaque refresh token. */
  newRefreshToken(): { raw: string; digest: string; expiresAt: Date } {
    const raw = randomOpaqueToken(64);
    const days = this.config.get<number>('jwt.refreshTtlDays', 30);
    const expiresAt = new Date(Date.now() + days * 24 * 60 * 60 * 1000);
    return { raw, digest: sha256Hex(raw), expiresAt };
  }

  digestRefreshToken(raw: string): string {
    return sha256Hex(raw);
  }

  /** Raw one-time token (email verify / password reset) + its digest. */
  newOneTimeToken(): { raw: string; digest: string } {
    const raw = randomOpaqueToken(48);
    return { raw, digest: sha256Hex(raw) };
  }
}

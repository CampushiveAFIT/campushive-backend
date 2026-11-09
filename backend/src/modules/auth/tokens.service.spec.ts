import { JwtService } from '@nestjs/jwt';
import { ConfigService } from '@nestjs/config';
import configuration from '../../config/configuration';
import { TokensService } from './tokens.service';
import { sha256Hex } from '../../common/crypto/token-hash.util';

function makeTokensService(ttl = '15m', refreshDays = '30'): TokensService {
  process.env.ACCESS_TOKEN_TTL = ttl;
  process.env.REFRESH_TOKEN_TTL_DAYS = refreshDays;
  process.env.JWT_ACCESS_SECRET = 'unit-test-access-secret';
  process.env.JWT_REFRESH_SECRET = 'unit-test-refresh-secret';
  const config = new ConfigService(configuration());
  return new TokensService(new JwtService({}), config);
}

describe('TokensService', () => {
  it('signs an access JWT with sub/role/jti and ~15m expiry', () => {
    const svc = makeTokensService('15m');
    const token = svc.signAccessToken({ id: 'user-1', role: 'STUDENT' });
    const decoded = new JwtService({}).decode(token) as Record<string, unknown>;
    expect(decoded.sub).toBe('user-1');
    expect(decoded.role).toBe('STUDENT');
    expect(typeof decoded.jti).toBe('string');
    expect((decoded.exp as number) - (decoded.iat as number)).toBe(900);
  });

  it('creates opaque refresh tokens whose digest matches sha256(raw)', () => {
    const svc = makeTokensService();
    const { raw, digest, expiresAt } = svc.newRefreshToken();
    expect(raw).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(digest).toBe(sha256Hex(raw));
    const thirtyDaysMs = 30 * 24 * 60 * 60 * 1000;
    expect(expiresAt.getTime() - Date.now()).toBeGreaterThan(thirtyDaysMs - 5000);
  });

  it('creates one-time tokens with matching digests', () => {
    const svc = makeTokensService();
    const { raw, digest } = svc.newOneTimeToken();
    expect(digest).toBe(sha256Hex(raw));
    expect(raw).not.toBe('');
  });
});

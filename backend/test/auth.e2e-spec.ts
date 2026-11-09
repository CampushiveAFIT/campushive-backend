import { INestApplication } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import { Test, TestingModule } from '@nestjs/testing';
import request from 'supertest';
import type { Response } from 'supertest';
import { AppModule } from '../src/app.module';
import { PrismaService } from '../src/config/prisma.service';
import { EmailService } from '../src/common/email/email.service';
import { REDIS_CLIENT } from '../src/common/redis/redis.module';
import type Redis from 'ioredis';

const PASSWORD = 'Str0ng-Passw0rd!';

describe('Auth + Users E2E (Slice 1)', () => {
  let app: INestApplication;
  let prisma: PrismaService;
  let redis: Redis;
  let jwt: JwtService;
  let config: ConfigService;

  const verifySent: { to: string; token: string }[] = [];
  const resetSent: { to: string; token: string }[] = [];

  const tokenFromUrl = (url: string): string => {
    const u = new URL(url, 'http://localhost');
    const t = u.searchParams.get('token');
    if (!t) throw new Error(`no token in url: ${url}`);
    return t;
  };

  const emailDouble = {
    sendVerificationEmail: async (to: string, url: string) => {
      verifySent.push({ to, token: tokenFromUrl(url) });
    },
    sendPasswordResetEmail: async (to: string, url: string) => {
      resetSent.push({ to, token: tokenFromUrl(url) });
    },
  };

  beforeAll(async () => {
    const module: TestingModule = await Test.createTestingModule({
      imports: [AppModule],
    })
      .overrideProvider(EmailService)
      .useValue(emailDouble)
      .compile();

    app = module.createNestApplication();
    app.setGlobalPrefix('api/v1');
    await app.init();

    prisma = app.get(PrismaService);
    redis = app.get(REDIS_CLIENT);
    jwt = app.get(JwtService);
    config = app.get(ConfigService);
  });

  afterAll(async () => {
    await app.close();
  });

  beforeEach(async () => {
    // Isolated state per test: truncate auth tables, flush rate-limit keys.
    await prisma.refreshToken.deleteMany();
    await prisma.emailVerificationToken.deleteMany();
    await prisma.passwordResetToken.deleteMany();
    await prisma.user.deleteMany();
    verifySent.length = 0;
    resetSent.length = 0;
    await redis.flushdb();
  });

  const api = () => request(app.getHttpServer());

  async function register(
    email: string,
    extra: Record<string, unknown> = {},
    expectedStatus = 201,
  ) {
    return api()
      .post('/api/v1/auth/register')
      .send({ email, password: PASSWORD, displayName: 'Test User', ...extra })
      .expect(expectedStatus);
  }

  function login(email: string, password = PASSWORD) {
    return api().post('/api/v1/auth/login').send({ email, password });
  }

  // ------------------------------------------------------------ registration
  describe('POST /api/v1/auth/register', () => {
    it('registers successfully with safe user shape and no tokens', async () => {
      const res = await register('adaeze@example.com');
      expect(res.body.success).toBe(true);
      const user = res.body.data;
      expect(user.email).toBe('adaeze@example.com');
      expect(user.role).toBe('STUDENT');
      expect(user.verificationStatus).toBe('UNVERIFIED');
      expect(user.passwordHash).toBeUndefined();
      expect(res.body.data.tokens).toBeUndefined();
      expect(res.body.accessToken).toBeUndefined();
      expect(verifySent).toHaveLength(1);
      expect(verifySent[0].to).toBe('adaeze@example.com');
    });

    it('rejects duplicate email with DUPLICATE_EMAIL', async () => {
      await register('dupe@example.com');
      const res = await register('dupe@example.com', {}, 409);
      expect(res.body.success).toBe(false);
      expect(res.body.error.code).toBe('DUPLICATE_EMAIL');
    });

    it('rejects invalid email with VALIDATION_ERROR', async () => {
      const res = await api()
        .post('/api/v1/auth/register')
        .send({ email: 'not-an-email', password: PASSWORD })
        .expect(400);
      expect(res.body.error.code).toBe('VALIDATION_ERROR');
    });

    it('rejects weak password with VALIDATION_ERROR', async () => {
      const res = await api()
        .post('/api/v1/auth/register')
        .send({ email: 'weak@example.com', password: 'password' })
        .expect(400);
      expect(res.body.error.code).toBe('VALIDATION_ERROR');
    });

    it('rejects unknown fields (whitelist)', async () => {
      const res = await api()
        .post('/api/v1/auth/register')
        .send({ email: 'x@example.com', password: PASSWORD, nickname: 'x' })
        .expect(400);
      expect(res.body.error.code).toBe('VALIDATION_ERROR');
    });

    it('rejects client-supplied role (no self-promotion)', async () => {
      const res = await api()
        .post('/api/v1/auth/register')
        .send({
          email: 'sneaky@example.com',
          password: PASSWORD,
          role: 'UNIVERSITY_ADMIN',
        })
        .expect(400);
      expect(res.body.error.code).toBe('VALIDATION_ERROR');
    });
  });

  // ------------------------------------------------------------------- login
  describe('POST /api/v1/auth/login', () => {
    it('logs in and returns access + refresh tokens', async () => {
      await register('login@example.com');
      const res = await login('login@example.com').expect(200);
      expect(res.body.success).toBe(true);
      expect(typeof res.body.data.accessToken).toBe('string');
      expect(typeof res.body.data.refreshToken).toBe('string');
      expect(res.body.data.user.email).toBe('login@example.com');
      expect(res.body.data.user.passwordHash).toBeUndefined();
    });

    it('rejects invalid password with INVALID_CREDENTIALS', async () => {
      await register('badpass@example.com');
      const res = await login('badpass@example.com', 'Wr0ng-Pass1!').expect(401);
      expect(res.body.error.code).toBe('INVALID_CREDENTIALS');
    });

    it('rejects unknown email with the same code (no enumeration)', async () => {
      const res = await login('nobody-here@example.com').expect(401);
      expect(res.body.error.code).toBe('INVALID_CREDENTIALS');
    });

    it('rejects inactive users', async () => {
      await register('inactive@example.com');
      await prisma.user.update({
        where: { email: 'inactive@example.com' },
        data: { isActive: false },
      });
      const res = await login('inactive@example.com').expect(401);
      expect(res.body.error.code).toBe('INVALID_CREDENTIALS');
    });
  });

  // ------------------------------------------------------------- access JWT
  describe('GET /api/v1/users/me (access token)', () => {
    it('returns profile for a valid token', async () => {
      await register('me@example.com');
      const { accessToken } = (await login('me@example.com')).body.data;
      const res = await api()
        .get('/api/v1/users/me')
        .set('Authorization', `Bearer ${accessToken}`)
        .expect(200);
      expect(res.body.data.email).toBe('me@example.com');
      expect(res.body.data.passwordHash).toBeUndefined();
    });

    it('rejects missing token', async () => {
      const res = await api().get('/api/v1/users/me').expect(401);
      expect(res.body.success).toBe(false);
    });

    it('rejects malformed token', async () => {
      const res = await api()
        .get('/api/v1/users/me')
        .set('Authorization', 'Bearer not-a-token')
        .expect(401);
      expect(res.body.success).toBe(false);
    });

    it('rejects expired token', async () => {
      await register('exp@example.com');
      const user = await prisma.user.findUniqueOrThrow({
        where: { email: 'exp@example.com' },
      });
      const expired = jwt.sign(
        {
          sub: user.id,
          role: user.role,
          jti: 'test-jti',
          exp: Math.floor(Date.now() / 1000) - 10,
        },
        { secret: config.get<string>('jwt.accessSecret') },
      );
      await api()
        .get('/api/v1/users/me')
        .set('Authorization', `Bearer ${expired}`)
        .expect(401);
    });
  });

  // ----------------------------------------------------------------- refresh
  describe('POST /api/v1/auth/refresh', () => {
    it('rotates: new tokens work; reuse of an old token burns the family', async () => {
      await register('rot@example.com');
      const first = (await login('rot@example.com')).body.data;

      // First rotation: old -> new
      const res = await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: first.refreshToken })
        .expect(200);
      expect(res.body.data.accessToken).toBeDefined();
      expect(res.body.data.refreshToken).not.toBe(first.refreshToken);

      // Chained rotation proves the new token works
      const chained = await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: res.body.data.refreshToken })
        .expect(200);
      expect(chained.body.data.refreshToken).not.toBe(
        res.body.data.refreshToken,
      );

      // Replaying the original (already-rotated) token: rejected as reuse,
      // and the whole token family is revoked (theft containment).
      const reuse = await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: first.refreshToken })
        .expect(401);
      expect(reuse.body.error.code).toBe('REVOKED_TOKEN');

      // The latest token from the same family is now dead too.
      await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: chained.body.data.refreshToken })
        .expect(401);
    });

    it('rejects expired refresh tokens', async () => {
      await register('expref@example.com');
      const { refreshToken } = (await login('expref@example.com')).body.data;
      const { sha256Hex } = await import(
        '../src/common/crypto/token-hash.util'
      );
      await prisma.refreshToken.update({
        where: { tokenHash: sha256Hex(refreshToken) },
        data: { expiresAt: new Date(Date.now() - 1000) },
      });
      const res = await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken })
        .expect(401);
      expect(res.body.error.code).toBe('EXPIRED_TOKEN');
    });

    it('rejects unknown refresh tokens', async () => {
      const res = await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: 'a'.repeat(86) })
        .expect(401);
      expect(res.body.error.code).toBe('REVOKED_TOKEN');
    });

    it('rejects refresh for inactive users', async () => {
      await register('deact@example.com');
      const { refreshToken } = (await login('deact@example.com')).body.data;
      await prisma.user.update({
        where: { email: 'deact@example.com' },
        data: { isActive: false },
      });
      await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken })
        .expect(401);
    });
  });

  // ------------------------------------------------- multi-session + logout
  describe('sessions: POST /api/v1/auth/logout', () => {
    it('supports concurrent sessions; logout revokes only current session', async () => {
      await register('multi@example.com');
      const deviceA = (await login('multi@example.com')).body.data;
      const deviceB = (await login('multi@example.com')).body.data;
      expect(deviceA.refreshToken).not.toBe(deviceB.refreshToken);

      // Refresh A works (rotation)
      const rotatedA = (
        await api()
          .post('/api/v1/auth/refresh')
          .send({ refreshToken: deviceA.refreshToken })
          .expect(200)
      ).body.data;

      // Logout A (current rotated session)
      const out = await api()
        .post('/api/v1/auth/logout')
        .send({ refreshToken: rotatedA.refreshToken })
        .expect(200);
      expect(out.body.data.revoked).toBe(true);

      // A session is dead
      await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: rotatedA.refreshToken })
        .expect(401);

      // B session still works
      await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: deviceB.refreshToken })
        .expect(200);
    });

    it('logout of an already-revoked token reports revoked:false', async () => {
      await register('out2@example.com');
      const { refreshToken } = (await login('out2@example.com')).body.data;
      await api().post('/api/v1/auth/logout').send({ refreshToken }).expect(200);
      const again = await api()
        .post('/api/v1/auth/logout')
        .send({ refreshToken })
        .expect(200);
      expect(again.body.data.revoked).toBe(false);
    });
  });

  // ---------------------------------------------------------- password change
  describe('POST /api/v1/auth/password/change', () => {
    it('changes password and invalidates sessions', async () => {
      await register('chg@example.com');
      const { accessToken, refreshToken } = (await login('chg@example.com'))
        .body.data;
      await api()
        .post('/api/v1/auth/password/change')
        .set('Authorization', `Bearer ${accessToken}`)
        .send({ currentPassword: PASSWORD, newPassword: 'N3w-Str0ng-Pass!' })
        .expect(200);

      // Old password no longer works, new one does
      await login('chg@example.com', PASSWORD).expect(401);
      await login('chg@example.com', 'N3w-Str0ng-Pass!').expect(200);

      // Old refresh session invalidated
      const res = await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken })
        .expect(401);
      expect(res.body.error.code).toBe('REVOKED_TOKEN');
    });

    it('rejects incorrect current password', async () => {
      await register('chg2@example.com');
      const { accessToken } = (await login('chg2@example.com')).body.data;
      await api()
        .post('/api/v1/auth/password/change')
        .set('Authorization', `Bearer ${accessToken}`)
        .send({ currentPassword: 'Wr0ng-Pass1!', newPassword: 'N3w-Str0ng-Pass!' })
        .expect(401);
    });

    it('requires authentication', async () => {
      await api()
        .post('/api/v1/auth/password/change')
        .send({ currentPassword: PASSWORD, newPassword: 'N3w-Str0ng-Pass!' })
        .expect(401);
    });
  });

  // ----------------------------------------------------------- password reset
  describe('password reset', () => {
    it('returns generic success for unknown email (no enumeration)', async () => {
      const res = await api()
        .post('/api/v1/auth/password/reset/request')
        .send({ email: 'ghost@example.com' })
        .expect(200);
      expect(res.body.data.requested).toBe(true);
      expect(resetSent).toHaveLength(0);
    });

    it('full reset flow invalidates sessions and tokens are single-use', async () => {
      await register('rst@example.com');
      const { refreshToken } = (await login('rst@example.com')).body.data;

      await api()
        .post('/api/v1/auth/password/reset/request')
        .send({ email: 'rst@example.com' })
        .expect(200);
      expect(resetSent).toHaveLength(1);

      await api()
        .post('/api/v1/auth/password/reset/confirm')
        .send({ token: resetSent[0].token, newPassword: 'R3set-Str0ng-Pass!' })
        .expect(200);

      // New password works, old does not
      await login('rst@example.com', PASSWORD).expect(401);
      await login('rst@example.com', 'R3set-Str0ng-Pass!').expect(200);

      // Old session invalidated
      await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken })
        .expect(401);

      // Token reuse rejected
      await api()
        .post('/api/v1/auth/password/reset/confirm')
        .send({ token: resetSent[0].token, newPassword: 'An0ther-Pass1!' })
        .expect(400);
    });

    it('rejects invalid and expired reset tokens', async () => {
      await api()
        .post('/api/v1/auth/password/reset/confirm')
        .send({ token: 'b'.repeat(86), newPassword: 'An0ther-Pass1!' })
        .expect(400);

      await register('rstexp@example.com');
      await api()
        .post('/api/v1/auth/password/reset/request')
        .send({ email: 'rstexp@example.com' })
        .expect(200);
      const { sha256Hex } = await import(
        '../src/common/crypto/token-hash.util'
      );
      await prisma.passwordResetToken.update({
        where: { tokenHash: sha256Hex(resetSent[0].token) },
        data: { expiresAt: new Date(Date.now() - 1000) },
      });
      await api()
        .post('/api/v1/auth/password/reset/confirm')
        .send({ token: resetSent[0].token, newPassword: 'An0ther-Pass1!' })
        .expect(400);
    });
  });

  // -------------------------------------------------------- email verification
  describe('email verification', () => {
    it('verifies email with a valid token', async () => {
      await register('ver@example.com');
      expect(verifySent).toHaveLength(1);
      const res = await api()
        .post('/api/v1/auth/email/verify/confirm')
        .send({ token: verifySent[0].token })
        .expect(200);
      expect(res.body.data.emailVerifiedAt).toBeTruthy();
      expect(res.body.data.verificationStatus).toBe('VERIFIED');
    });

    it('rejects invalid, expired, and reused tokens', async () => {
      await api()
        .post('/api/v1/auth/email/verify/confirm')
        .send({ token: 'c'.repeat(86) })
        .expect(400);

      await register('verexp@example.com');
      const { sha256Hex } = await import(
        '../src/common/crypto/token-hash.util'
      );
      await prisma.emailVerificationToken.update({
        where: { tokenHash: sha256Hex(verifySent[0].token) },
        data: { expiresAt: new Date(Date.now() - 1000) },
      });
      await api()
        .post('/api/v1/auth/email/verify/confirm')
        .send({ token: verifySent[0].token })
        .expect(400);

      // Reuse after success
      await register('veruse@example.com');
      const token = verifySent[verifySent.length - 1].token;
      await api()
        .post('/api/v1/auth/email/verify/confirm')
        .send({ token })
        .expect(200);
      await api()
        .post('/api/v1/auth/email/verify/confirm')
        .send({ token })
        .expect(400);
    });
  });

  // ----------------------------------------------------------------- profile
  describe('PATCH /api/v1/users/me', () => {
    it('updates permitted profile fields', async () => {
      await register('prof@example.com');
      const { accessToken } = (await login('prof@example.com')).body.data;
      const res = await api()
        .patch('/api/v1/users/me')
        .set('Authorization', `Bearer ${accessToken}`)
        .send({
          displayName: 'Adaeze O.',
          bio: 'Final-year CS student.',
          avatarUrl: 'https://cdn.example.com/a.png',
        })
        .expect(200);
      expect(res.body.data.displayName).toBe('Adaeze O.');
      expect(res.body.data.bio).toBe('Final-year CS student.');
      expect(res.body.data.passwordHash).toBeUndefined();
    });

    it('rejects role escalation', async () => {
      await register('esc@example.com');
      const { accessToken } = (await login('esc@example.com')).body.data;
      await api()
        .patch('/api/v1/users/me')
        .set('Authorization', `Bearer ${accessToken}`)
        .send({ displayName: 'x', role: 'UNIVERSITY_ADMIN' })
        .expect(400);
      const me = await api()
        .get('/api/v1/users/me')
        .set('Authorization', `Bearer ${accessToken}`)
        .expect(200);
      expect(me.body.data.role).toBe('STUDENT');
    });

    it('rejects verification escalation and arbitrary fields', async () => {
      await register('esc2@example.com');
      const { accessToken } = (await login('esc2@example.com')).body.data;
      await api()
        .patch('/api/v1/users/me')
        .set('Authorization', `Bearer ${accessToken}`)
        .send({ verificationStatus: 'VERIFIED', isActive: false })
        .expect(400);
    });

    it('requires authentication', async () => {
      await api().patch('/api/v1/users/me').send({ bio: 'hi' }).expect(401);
    });
  });

  // ------------------------------------------------------- routes + limits
  describe('API versioning and rate limiting', () => {
    it('does not expose /api/api/v1 duplication', async () => {
      await api().get('/api/api/v1/users/me').expect(404);
    });

    it('rate-limits registration after 5 attempts per 15 minutes', async () => {
      for (let i = 0; i < 5; i++) {
        await api()
          .post('/api/v1/auth/register')
          .send({
            email: `rl${i}@example.com`,
            password: PASSWORD,
          })
          .expect(201);
      }
      const limited = await api()
        .post('/api/v1/auth/register')
        .send({ email: 'rl5@example.com', password: PASSWORD })
        .expect(429);
      expect(limited.body.error.code).toBe('RATE_LIMITED');
      expect(limited.headers['x-ratelimit-limit']).toBe('5');
    });
  });

  // ------------------------------------------- adversarial regression tests
  describe('Adversarial: concurrency, spoofing, races, empty PATCH', () => {
    it('A1: concurrent refresh of one token yields exactly one successor', async () => {
      await register('race@example.com');
      const { refreshToken } = (await login('race@example.com')).body.data;

      const attempts = await Promise.allSettled(
        Array.from({ length: 5 }, () =>
          api().post('/api/v1/auth/refresh').send({ refreshToken }),
        ),
      );
      const succeeded = attempts.filter(
        (a): a is PromiseFulfilledResult<Response> =>
          a.status === 'fulfilled' && a.value.status === 200,
      );
      // Exactly one rotation may succeed; concurrent replays are rejected.
      expect(succeeded).toHaveLength(1);
      for (const a of attempts) {
        if (a.status === 'fulfilled' && a.value.status !== 200) {
          expect(a.value.status).toBe(401);
          expect(a.value.body.error.code).toBe('REVOKED_TOKEN');
        }
      }

      const winner = (
        succeeded[0].value.body as {
          data: { refreshToken: string; accessToken: string };
        }
      ).data;
      expect(typeof winner.refreshToken).toBe('string');
      expect(winner.refreshToken).not.toBe(refreshToken);

      // The losers' replays triggered family burn (theft containment), so the
      // winner's token is dead too: no second generation can ever exist.
      await api()
        .post('/api/v1/auth/refresh')
        .send({ refreshToken: winner.refreshToken })
        .expect(401);

      // DB state: exactly one successor was ever minted; nothing is active.
      const family = await prisma.refreshToken.findMany({
        select: { revokedAt: true, replacedByTokenId: true },
      });
      expect(family).toHaveLength(2);
      expect(family.filter((t) => !t.revokedAt)).toHaveLength(0);
      expect(
        family.filter((t) => t.replacedByTokenId).map((t) => t.replacedByTokenId),
      ).toHaveLength(1);
    });

    it('A2: rotating X-Forwarded-For must not evade the register limit', async () => {
      for (let i = 0; i < 5; i++) {
        await api()
          .post('/api/v1/auth/register')
          .set('X-Forwarded-For', `10.9.0.${i}`)
          .send({ email: `xff${i}@example.com`, password: PASSWORD })
          .expect(201);
      }
      // Sixth attempt from a "different" spoofed IP must still be limited:
      // the proxy is untrusted by default, so all share the real client bucket.
      const limited = await api()
        .post('/api/v1/auth/register')
        .set('X-Forwarded-For', '10.9.0.99')
        .send({ email: 'xff5@example.com', password: PASSWORD })
        .expect(429);
      expect(limited.body.error.code).toBe('RATE_LIMITED');
    });

    it('A2b: with an explicitly trusted proxy, XFF determines the bucket', async () => {
      const httpAdapter = app.getHttpAdapter().getInstance() as {
        set: (key: string, value: unknown) => void;
      };
      httpAdapter.set('trust proxy', 1);
      try {
        // Distinct trusted client IPs get distinct buckets: all succeed.
        for (let i = 0; i < 5; i++) {
          await api()
            .post('/api/v1/auth/register')
            .set('X-Forwarded-For', `10.8.0.${i}`)
            .send({ email: `pxff${i}@example.com`, password: PASSWORD })
            .expect(201);
        }
        // Six rapid hits from one trusted IP are limited.
        for (let i = 0; i < 5; i++) {
          await api()
            .post('/api/v1/auth/register')
            .set('X-Forwarded-For', '10.8.9.9')
            .send({ email: `pxffs${i}@example.com`, password: PASSWORD })
            .expect(201);
        }
        await api()
          .post('/api/v1/auth/register')
          .set('X-Forwarded-For', '10.8.9.9')
          .send({ email: 'pxffs5@example.com', password: PASSWORD })
          .expect(429);
      } finally {
        httpAdapter.set('trust proxy', false);
      }
    });

    it('A3: concurrent duplicate registrations yield one 201 and one 409', async () => {
      const results = await Promise.allSettled([
        api()
          .post('/api/v1/auth/register')
          .send({ email: 'dupe-race@example.com', password: PASSWORD }),
        api()
          .post('/api/v1/auth/register')
          .send({ email: 'dupe-race@example.com', password: PASSWORD }),
      ]);
      const statuses = results.map((r) =>
        r.status === 'fulfilled' ? r.value.status : -1,
      );
      expect(statuses.sort()).toEqual([201, 409]);
      const loser = results.find(
        (r): r is PromiseFulfilledResult<Response> =>
          r.status === 'fulfilled' && r.value.status === 409,
      );
      expect(
        (loser!.value.body as { error: { code: string } }).error.code,
      ).toBe('DUPLICATE_EMAIL');
      // DB remains consistent: exactly one user.
      expect(
        await prisma.user.count({
          where: { email: 'dupe-race@example.com' },
        }),
      ).toBe(1);
    });

    it('A4: PATCH /me with {} returns 200 with unchanged profile', async () => {
      await register('empty-patch@example.com');
      const { accessToken } = (await login('empty-patch@example.com')).body
        .data;
      const before = (
        await api()
          .get('/api/v1/users/me')
          .set('Authorization', `Bearer ${accessToken}`)
          .expect(200)
      ).body.data;
      const res = await api()
        .patch('/api/v1/users/me')
        .set('Authorization', `Bearer ${accessToken}`)
        .send({})
        .expect(200);
      expect(res.body.data).toEqual(before);
      expect(res.body.data.passwordHash).toBeUndefined();
    });
    it('rejects oversized bodies with 413 PAYLOAD_TOO_LARGE', async () => {
      const res = await api()
        .post('/api/v1/auth/register')
        .send({ email: 'big@example.com', password: 'x'.repeat(200 * 1024) })
        .expect(413);
      expect(res.body.success).toBe(false);
      expect(res.body.error.code).toBe('PAYLOAD_TOO_LARGE');
    });
  });
});

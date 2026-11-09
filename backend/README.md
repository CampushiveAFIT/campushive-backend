# Campus Hive Backend

NestJS + TypeScript backend for Campus Hive (Foundation Slice 1: Identity + Authentication).

## Stack (exact versions)

| Component | Version |
|---|---|
| Node.js | 26.7.0 (dev) / >= 20 required |
| TypeScript | 5.9.3 |
| NestJS (`common`, `core`, `platform-express`) | 11.2.3 |
| `@nestjs/config` | 4.0.4 |
| `@nestjs/jwt` / `@nestjs/passport` / `@nestjs/swagger` | 11.0.2 / 11.0.5 / 11.4.7 |
| Prisma (`prisma`, `@prisma/client`) | 6.19.3 |
| PostgreSQL | 18.4 (server) |
| Redis (server binary for dev/test) | 8.10.1 |
| `ioredis` | 5.11.1 |
| `argon2` (Argon2id password hashing) | 0.44.0 |
| `helmet` | 8.3.0 |
| `class-validator` / `class-transformer` | 0.14.4 / 0.5.1 |
| Jest / ts-jest / supertest | 29.7.0 / 29.4.12 / 7.2.2 |
| ESLint / typescript-eslint | 10.10.0 / 8.70.0 |
| `redis-memory-server` (dev/test only) | 0.12.1 |

## Quick start (new developer)

```bash
cd campus-hive/backend
npm install

# 1. Environment
cp .env.example .env
# Fill in DATABASE_URL and generate JWT secrets:
node -e "console.log(require('crypto').randomBytes(64).toString('hex'))"

# 2. Database (PostgreSQL must be running)
createdb campus_hive
npx prisma migrate deploy
npx prisma generate

# 3. Dev server
npm run start:dev
# API: http://localhost:3000/api/v1  |  Docs: http://localhost:3000/api/docs
```

## Scripts

| Script | Purpose |
|---|---|
| `npm run build` | Compile TypeScript (`nest build`) |
| `npm run start:dev` | Watch-mode dev server |
| `npm run start:prod` | Run compiled `dist/main` |
| `npm run lint` | ESLint over `src/` + `test/` |
| `npm test` | Unit tests (Jest) |
| `npm run test:e2e` | E2E tests against isolated `campus_hive_test` DB |
| `npx prisma validate` | Validate Prisma schema |
| `npx prisma migrate status` | Check migration state |
| `npx prisma migrate dev --name <x>` | Create + apply migration (dev) |
| `npx prisma migrate deploy` | Apply migrations (production) |

## Authentication architecture

- **Password → Argon2id → `passwordHash`.** Never returned by the API.
- **Access token:** short-lived signed JWT (`15m` default), claims `{ sub, role, jti }`.
  Sent as `Authorization: Bearer <token>`.
- **Refresh token:** opaque 64-byte `randomBytes(...).toString('base64url')` value.
  Only its **SHA-256 digest** is stored (`RefreshToken.tokenHash`, unique).
  Lifetime 30 days (`expiresAt` enforced server-side).
- **Rotation:** every successful `POST /api/v1/auth/refresh` atomically claims
  the presented token (conditional `UPDATE … WHERE revoked_at IS NULL`), then
  revokes it and issues a successor in one transaction. Concurrent replays of
  one token yield exactly one successor; losers are treated as token reuse.
- **Reuse detection:** presenting an already-rotated token revokes the entire
  token family (theft containment) and returns 401.
- **Multiple sessions:** supported (`userId` is intentionally NOT unique on
  `RefreshToken`). Logout revokes only the presented session.
- **Registration does NOT authenticate:** `POST /auth/register` creates the account
  (`role=STUDENT`, `verificationStatus=UNVERIFIED`) and sends a verification email.
  Login issues tokens.
- **Transport:** JSON responses carry tokens in this slice. For production, prefer
  migrating refresh tokens to `HttpOnly; Secure; SameSite` cookies (documented
  tradeoff: JSON is simpler for the React client now; `localStorage` storage of
  long-lived refresh tokens is discouraged).
- **Frontend storage expectations (George):** keep the access token in memory,
  persist the refresh token in the most secure storage available, call
  `POST /api/v1/auth/refresh` on 401, and discard both tokens on logout.
  On `REVOKED_TOKEN` during refresh, force a full re-login (possible theft).

## Email vs university verification

- **Email verification** (`emailVerifiedAt`, 48h single-use token) proves the user
  controls the address. It is NOT institutional verification.
- **University verification** belongs to Slice 2+. `verificationStatus` exists on the
  model but Slice 1 only transitions `UNVERIFIED → VERIFIED` via email confirmation.

## Rate limiting (Redis, atomic Lua)

Backed by a real Redis server in every environment:

- Production: `REDIS_URL` is **required** (fail-fast at startup otherwise).
- Development/test without `REDIS_URL`: an embedded real `redis-server` binary
  is started automatically (`bin/redis-server`, gitignored; `REDISMS_SYSTEM_BINARY`
  override supported). This is a genuine Redis server, not a mock.

Limits: register 5/15m, login 5/15m, refresh 10/15m, password-reset 3/15m,
email-verify 3/15m (per IP). Exceeded requests receive `429 RATE_LIMITED` with
`X-RateLimit-*` headers. If Redis is unreachable the limiter fails **open**
(allows the request, logs a warning) — rate limiting is defense-in-depth;
authentication correctness never depends on Redis.

## CORS / proxy

- `CORS_ORIGIN`: comma-separated allowlist (default `http://localhost:5173` for Vite).
  Never `*` for credentialed auth.
- `TRUST_PROXY`: Express `trust proxy` setting (default `loopback`). Set to the
  number of trusted proxies (e.g. `1`) when deployed behind a reverse proxy so
  rate-limit IPs are correct. Do not trust arbitrary `X-Forwarded-For` otherwise.

## Response contract

Success: `{ "success": true, "data": {…} }`.
Error: `{ "success": false, "error": { "code", "message", "details?" } }`.
Codes: `VALIDATION_ERROR`, `INVALID_CREDENTIALS`, `DUPLICATE_EMAIL`,
`EXPIRED_TOKEN`, `REVOKED_TOKEN`, `WEAK_PASSWORD`, `NOT_FOUND`, `FORBIDDEN`,
`RATE_LIMITED`, `CONFLICT`, `PAYLOAD_TOO_LARGE`, `INTERNAL_ERROR`. Stack traces and DB internals are
never sent to clients.

## API endpoints (Slices 1–2)

```text
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
POST /api/v1/auth/password/change            (Bearer)
POST /api/v1/auth/password/reset/request
POST /api/v1/auth/password/reset/confirm
POST /api/v1/auth/email/verify/request
POST /api/v1/auth/email/verify/confirm
GET  /api/v1/users/me                        (Bearer)
PATCH /api/v1/users/me                       (Bearer)
GET  /api/v1/universities                    (Bearer)
GET  /api/v1/universities/:slug              (Bearer)
GET  /api/v1/universities/:slug/communities  (Bearer)
GET  /api/v1/users/me/university             (Bearer)
POST /api/v1/users/me/university             (Bearer)
PATCH /api/v1/users/me/university            (Bearer)
POST /api/v1/communities                     (Bearer)
GET  /api/v1/communities/:id                 (Bearer)
PATCH /api/v1/communities/:id                (Bearer, owner)
DELETE /api/v1/communities/:id               (Bearer, owner, deactivates)
POST /api/v1/communities/:id/join            (Bearer)
DELETE /api/v1/communities/:id/leave         (Bearer)
GET  /api/v1/communities/:id/members         (Bearer)
PATCH /api/v1/communities/:id/members/:userId (Bearer, owner)
DELETE /api/v1/communities/:id/members/:userId (Bearer, owner/moderator)
POST /api/v1/users/:id/follow                (Bearer)
DELETE /api/v1/users/:id/follow              (Bearer)
GET  /api/v1/users/:id/followers             (Bearer)
GET  /api/v1/users/:id/following             (Bearer)
POST /api/v1/users/:id/block                 (Bearer)
DELETE /api/v1/users/:id/block               (Bearer)
POST /api/v1/posts                           (Bearer)
GET  /api/v1/posts/:id                       (Bearer)
PATCH /api/v1/posts/:id                      (Bearer, author)
DELETE /api/v1/posts/:id                     (Bearer, author, soft delete)
GET  /api/v1/users/:id/posts                 (Bearer)
GET  /api/v1/universities/:slug/posts        (Bearer, members)
GET  /api/v1/communities/:id/posts           (Bearer)
POST /api/v1/posts/:id/like                  (Bearer)
DELETE /api/v1/posts/:id/like                (Bearer)
POST /api/v1/posts/:id/comments              (Bearer)
GET  /api/v1/posts/:id/comments              (Bearer)
PATCH /api/v1/comments/:id                   (Bearer, author)
DELETE /api/v1/comments/:id                  (Bearer, author or post owner)
GET  /api/v1/feed/home                       (Bearer)
GET  /api/v1/feed/university                 (Bearer, members)
GET  /api/v1/feed/community/:id              (Bearer)
GET  /api/v1/feed/explore                    (Bearer)
GET  /api/v1/search?q=&type=&limit=          (Bearer)
GET  /api/v1/notifications                   (Bearer)
GET  /api/v1/notifications/unread-count      (Bearer)
POST /api/v1/notifications/read-all          (Bearer)
POST /api/v1/notifications/:id/read         (Bearer, owner)
GET  /api/v1/users/me/notification-preferences (Bearer)
PATCH /api/v1/users/me/notification-preferences (Bearer)
```

Realtime: Socket.IO on the same origin. Connect with `auth: { token }`
(access JWT) to join your private room and receive `notification.created`
events. Origins follow `CORS_ORIGIN`. Multi-instance fan-out
(`@socket.io/redis-adapter`) is the documented future path, not yet wired.

OpenAPI: `/api/docs` (Swagger UI), `/api-json`.

## Tests

- Unit (`npm test`): crypto digests, Argon2id hashing, token issuance, TTL parsing,
  rate-limiter fail-open, cursor pagination, visibility rules.
- E2E (`npm run test:e2e`): full auth flows plus campus/social flows against the
  isolated `campus_hive_test` database (reset from migrations) + embedded real
  Redis. Covers registration, login, JWT validation, rotation, reuse burn,
  multi-session, logout, password change/reset, email verification, profile
  security, versioning, rate limits, universities, memberships, communities,
  follows, blocks, posts, visibility, likes, comments, and hostile payloads.

## Security notes

- `.env` is gitignored; `.env.example` contains placeholders only.
- Legacy FESTEJO credentials (`example/.env`) are compromised: rotate/revoke them
  outside this project and never copy them here.
- Production checklist: `NODE_ENV=production`, `REDIS_URL` set, real `DATABASE_URL`,
  strong JWT secrets, `CORS_ORIGIN` set to the frontend origin, `TRUST_PROXY`
  configured, TLS termination in front of the app.

# Campus Hive Go backend

Go rewrite of the Campus Hive API (`../backend`, NestJS + Prisma). The module is
`campus-hive/backend-go`; internal packages are not importable from outside the
module.

## Requirements

- Go 1.26+
- PostgreSQL 15+ reachable through `DATABASE_URL`
- Redis 7+ for rate limiting, presence and WebSocket fan-out. `REDIS_URL` is
  required in production. In development an embedded Redis server is started
  automatically when `REDIS_URL` is unset and `redis-server` is on `PATH` or
  `REDIS_SERVER_BINARY` points at a binary.

## Configuration

Every variable is read from the environment; `.env` is loaded when present. See
`.env.example` for the full list and defaults.

| Variable | Default | Notes |
| --- | --- | --- |
| `NODE_ENV` | `development` | `production` enables production-only validation |
| `PORT` | `3000` | |
| `DATABASE_URL` | — | required |
| `JWT_ACCESS_SECRET` | — | required |
| `JWT_REFRESH_SECRET` | — | required |
| `ACCESS_TOKEN_TTL` | `15m` | accepts `s`, `m`, `h`, `d` suffixes |
| `REFRESH_TOKEN_TTL_DAYS` | `30` | |
| `REDIS_URL` | — | required in production |
| `REDIS_SERVER_BINARY` | auto-detected | dev/test only |
| `CORS_ORIGIN` | `http://localhost:5173` | comma separated; `*` is rejected |
| `TRUST_PROXY` | `loopback` | `loopback`, `linklocal`, `uniquelocal`, a CIDR, a hop count, `true` or `false` |
| `MAX_BODY_BYTES` | `1048576` | |
| `OUTBOX_POLL_INTERVAL_MS` | `2000` | |
| `AUTO_MIGRATE` | `false` | apply migrations on boot |
| `LOG_LEVEL` | `info` | |
| `EMAIL_FROM`, `EMAIL_HOST`, `EMAIL_PORT`, `EMAIL_USER`, `EMAIL_PASSWORD` | | SMTP delivery |

## Commands

```sh
make run               # start the API server
make build             # compile every package
make test              # run the full test suite
make test-race         # run the suite with the race detector
make vet fmt-check     # static checks
```

## Migrations

The migration files under `internal/migrations` are the Prisma migrations copied
byte for byte, paired with hand-written down migrations. Versions are the
original Prisma timestamps, so `schema_migrations` lines up with Prisma history.

```sh
make migrate-latest                       # newest embedded version
make migrate-up DATABASE_URL=...          # apply pending migrations
make migrate-down DATABASE_URL=...        # revert every migration
make migrate-reset DATABASE_URL=...       # revert, then re-apply
make migrate-version DATABASE_URL=...     # applied version and dirty flag
make migrate-force DATABASE_URL=...       # clear state after a failed run
```

A failed migration leaves the database dirty. Fix the cause, then either
`make migrate-force` (clears the recorded version and re-applies from scratch) or
drop the database and re-apply.

### Schema parity

`cmd/schemadiff` compares two live databases across enums, tables, columns,
indexes and constraints:

```sh
make schemadiff DATABASE_URL=... SCHEMA_REFERENCE_DATABASE_URL=...
```

The reference database is a virgin database migrated with
`prisma migrate deploy` from `../backend`.

## Tests

```sh
TEST_DATABASE_URL=postgresql://.../campus_hive_test \
TEST_MIGRATIONS_DATABASE_URL=postgresql://.../campus_hive_migrations_test \
  go test ./...
```

Database-backed tests skip when their database is unreachable. Each package that
resets the schema gets its own database, derived from `TEST_DATABASE_URL` by
appending the package name (for example `campus_hive_go_test_users`), so parallel
packages never tear down each other's tables. Set `TEST_DATABASE_SHARED=1` to
opt out. `internal/database` truncates application tables, while
`internal/migrations` drops and recreates the `public` schema in its own
`TEST_MIGRATIONS_DATABASE_URL` database.

`SCHEMA_REFERENCE_DATABASE_URL` enables the Prisma parity test; without it that
test is skipped.

## Layout

```text
cmd/server        HTTP server entrypoint and route registration
cmd/migrate       migration CLI
cmd/schemadiff    schema comparison CLI
internal/auth       authentication, password hashing, JWT and refresh tokens
internal/communities community creation, discovery, join/leave, role management
internal/config     environment configuration and trusted-proxy handling
internal/database   pgx pool, transaction helper, error classification
internal/email      SMTP and mock email delivery
internal/events     event creation, discovery, visibility, and ticket lifecycle
internal/feed       home, university, community, and explore feed aggregation
internal/graph      social follow and block graph
internal/httpx      envelopes, strict JSON, middleware, pagination
internal/httpserver router, middleware chain, health check, docs
internal/logging    logger and request context
internal/messaging  1:1 conversations, idempotent messaging, and unread tracking
internal/migrations embedded SQL and golang-migrate plumbing
internal/notifications notifications list, unread tracking, and preferences
internal/openapi    OpenAPI document and Swagger UI page
internal/posts      posts, comments, likes, and feed visibility rules
internal/ratelimit  Redis sliding-window limiter
internal/redisx     Redis client and embedded server helper
internal/routes     versioned route registration
internal/schemadiff catalog comparison
internal/search     case-insensitive multi-entity search
internal/testutil   test helpers
internal/universities university listing, details, and membership
internal/users      user profiles and current user context
internal/validate   request validation utilities
```

## Endpoints

| Path | Purpose |
| --- | --- |
| `GET /healthz` | database and Redis health |
| `GET /api/docs-json`, `GET /api-json` | OpenAPI document |
| `GET /api/docs` | Swagger UI |
| `POST /api/v1/auth/register`, `/login`, `/refresh`, `/logout` | sessions |
| `POST /api/v1/auth/password/{change,reset/request,reset/confirm}` | passwords |
| `POST /api/v1/auth/email/verify/{request,confirm}` | email verification |
| `GET`, `PATCH /api/v1/users/me` | current profile |
| `GET`, `POST`, `PATCH /api/v1/users/me/university` | campus identity |
| `GET /api/v1/users/{id}/posts` | an author's visible posts |
| `POST`, `DELETE /api/v1/users/{id}/follow` | follow graph |
| `GET /api/v1/users/{id}/followers`, `/following` | follow lists |
| `POST`, `DELETE /api/v1/users/{id}/block` | block graph |
| `GET /api/v1/universities` | active universities |
| `GET /api/v1/universities/{slug}` | university detail |
| `GET /api/v1/universities/{slug}/communities`, `/posts` | university feed |
| `POST /api/v1/communities` | create a community |
| `GET`, `PATCH`, `DELETE /api/v1/communities/{id}` | community detail/update/deactivate |
| `POST`, `DELETE /api/v1/communities/{id}/join`, `/leave` | membership |
| `GET /api/v1/communities/{id}/members`, `PATCH`, `DELETE .../{userId}` | roles and bans |
| `GET /api/v1/communities/{id}/posts` | community feed |
| `POST /api/v1/posts`, `GET`, `PATCH`, `DELETE /api/v1/posts/{id}` | posts |
| `POST`, `DELETE /api/v1/posts/{id}/like` | likes |
| `POST`, `GET /api/v1/posts/{id}/comments` | comments |
| `PATCH`, `DELETE /api/v1/comments/{id}` | comment author/owner actions |

Every Slice 2 route is protected by the access-token guard and applies the
same visibility rules as the NestJS reference (`post.visibility`, community and
university membership, follow and block relations). See `../parity/` for the
harness that replays identical requests against both stacks.

### Deliberate deviations

Two null-handling paths differ from the reference on purpose, because the
reference crashes (500) or raises, while the port has a defined answer:

- `PATCH /communities/{id}` with `"description": null` clears the description;
  the reference calls `.trim()` on `null` and answers 500.
- `PATCH /posts/{id}` with `"content": null` is a no-op; the reference forwards
  `null` to a required Prisma column and answers 500.

Both are recorded in `../parity/known_deviations.json` and exercised by the
`slice2` parity suite.

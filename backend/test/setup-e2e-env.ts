import * as path from 'path';

/**
 * Ran in every E2E worker BEFORE tests. Sets the isolated test environment:
 * - Dedicated test database (never the developer database).
 * - Fixed test JWT secrets.
 * - Embedded Redis binary path (real redis-server binary).
 */
process.env.NODE_ENV = 'test';
process.env.PORT = '3101';
process.env.DATABASE_URL =
  'postgresql://astro@localhost:5432/campus_hive_test?host=/run/postgresql';
process.env.JWT_ACCESS_SECRET =
  'e2e-test-access-secret-not-for-production-use-only-0123456789abcdef';
process.env.JWT_REFRESH_SECRET =
  'e2e-test-refresh-secret-not-for-production-use-only-0123456789abcdef';
process.env.ACCESS_TOKEN_TTL = '15m';
process.env.REFRESH_TOKEN_TTL_DAYS = '30';
process.env.CORS_ORIGIN = 'http://localhost:5173';
process.env.EMAIL_FROM = 'noreply@campushive.test';
// Unset any external Redis so tests use the embedded real server.
delete process.env.REDIS_URL;
// Point redis-memory-server at the prebuilt binary (no source build).
process.env.REDISMS_SYSTEM_BINARY = path.resolve(
  __dirname,
  '..',
  'bin',
  'redis-server',
);

/** Parse '15m' | '30s' | '2h' | '7d' (or plain seconds) into seconds. */
function parseTtlSeconds(raw: string, fallback: number): number {
  const m = /^\s*(\d+)\s*([smhd])?\s*$/i.exec(raw ?? '');
  if (!m) return fallback;
  const value = parseInt(m[1], 10);
  const unit = (m[2] ?? 's').toLowerCase();
  const mult = unit === 's' ? 1 : unit === 'm' ? 60 : unit === 'h' ? 3600 : 86400;
  return value * mult;
}

export default () => ({
  nodeEnv: process.env.NODE_ENV ?? 'development',
  port: parseInt(process.env.PORT ?? '3000', 10),
  databaseUrl: process.env.DATABASE_URL ?? '',
  jwt: {
    accessSecret: process.env.JWT_ACCESS_SECRET ?? '',
    refreshSecret: process.env.JWT_REFRESH_SECRET ?? '',
    accessTtlSec: parseTtlSeconds(process.env.ACCESS_TOKEN_TTL ?? '15m', 900),
    refreshTtlDays: parseInt(process.env.REFRESH_TOKEN_TTL_DAYS ?? '30', 10),
  },
  corsOrigins: (process.env.CORS_ORIGIN ?? 'http://localhost:5173')
    .split(',')
    .map((o) => o.trim())
    .filter(Boolean),
  redisUrl: process.env.REDIS_URL ?? '',
  email: {
    from: process.env.EMAIL_FROM ?? 'noreply@campushive.com',
    host: process.env.EMAIL_HOST ?? '',
    port: parseInt(process.env.EMAIL_PORT ?? '587', 10),
    user: process.env.EMAIL_USER ?? '',
    password: process.env.EMAIL_PASSWORD ?? '',
  },
});

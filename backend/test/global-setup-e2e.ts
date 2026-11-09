import { execSync } from 'child_process';
import * as path from 'path';

/**
 * Jest globalSetup: runs once in a separate process before workers start.
 * Resets the isolated test database from migrations (reproducible schema).
 */
export default async function globalSetup(): Promise<void> {
  process.env.DATABASE_URL =
    'postgresql://astro@localhost:5432/campus_hive_test?host=/run/postgresql';
  const backendDir = path.resolve(__dirname, '..');
  execSync('npx prisma migrate reset --force --skip-seed --skip-generate', {
    cwd: backendDir,
    stdio: 'inherit',
    env: { ...process.env },
  });
}

import { createHash, randomBytes } from 'crypto';

/**
 * Deterministic SHA-256 digest for opaque one-time tokens
 * (refresh tokens, email verification tokens, password reset tokens).
 *
 * Unlike Argon2 (salted, non-deterministic), SHA-256 of the same input
 * always yields the same digest, so it can be used as a database lookup key.
 * The raw token is never persisted.
 */
export function sha256Hex(input: string): string {
  return createHash('sha256').update(input, 'utf8').digest('hex');
}

/** Cryptographically secure random opaque token (base64url). */
export function randomOpaqueToken(bytes = 64): string {
  return randomBytes(bytes).toString('base64url');
}

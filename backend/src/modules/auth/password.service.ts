import { Injectable } from '@nestjs/common';
import * as argon2 from 'argon2';

/**
 * Password hashing. Argon2id ONLY:
 *   Password -> Argon2id -> passwordHash
 * Refresh tokens / one-time tokens use deterministic SHA-256 digests
 * (see token-hash.util) and must never go through this service.
 */
@Injectable()
export class PasswordService {
  async hash(plain: string): Promise<string> {
    return argon2.hash(plain, { type: argon2.argon2id });
  }

  async verify(hash: string, plain: string): Promise<boolean> {
    try {
      return await argon2.verify(hash, plain);
    } catch {
      return false;
    }
  }
}

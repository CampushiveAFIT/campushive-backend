import { randomOpaqueToken, sha256Hex } from './token-hash.util';

describe('token-hash.util', () => {
  it('sha256Hex is deterministic for the same input', () => {
    expect(sha256Hex('same-token')).toBe(sha256Hex('same-token'));
  });

  it('sha256Hex differs for different inputs', () => {
    expect(sha256Hex('token-a')).not.toBe(sha256Hex('token-b'));
  });

  it('sha256Hex produces a 64-char lowercase hex digest', () => {
    expect(sha256Hex('anything')).toMatch(/^[0-9a-f]{64}$/);
  });

  it('randomOpaqueToken is unique across generations', () => {
    const seen = new Set(Array.from({ length: 50 }, () => randomOpaqueToken()));
    expect(seen.size).toBe(50);
  });

  it('randomOpaqueToken is base64url with expected length', () => {
    // 64 bytes -> ceil(64/3)*4 = 88 chars, no padding in base64url
    const token = randomOpaqueToken(64);
    expect(token).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(token.length).toBe(86);
  });
});

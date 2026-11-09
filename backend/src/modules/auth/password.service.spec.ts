import { PasswordService } from './password.service';

describe('PasswordService (Argon2id)', () => {
  const service = new PasswordService();

  it('hashes and verifies the same password', async () => {
    const hash = await service.hash('Str0ng-Passw0rd!');
    expect(hash).not.toContain('Str0ng-Passw0rd!');
    expect(await service.verify(hash, 'Str0ng-Passw0rd!')).toBe(true);
  });

  it('rejects a wrong password', async () => {
    const hash = await service.hash('Str0ng-Passw0rd!');
    expect(await service.verify(hash, 'Wr0ng-Password!')).toBe(false);
  });

  it('produces different hashes for the same password (salted)', async () => {
    const a = await service.hash('Same-Passw0rd1');
    const b = await service.hash('Same-Passw0rd1');
    expect(a).not.toBe(b);
  });

  it('returns false for a malformed stored hash', async () => {
    await expect(service.verify('not-a-hash', 'whatever')).resolves.toBe(false);
  });
});

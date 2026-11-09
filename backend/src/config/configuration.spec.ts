import configuration from './configuration';

describe('configuration TTL parsing', () => {
  const OLD = { ...process.env };

  afterEach(() => {
    process.env = { ...OLD };
  });

  function ttl(raw: string): number {
    process.env.ACCESS_TOKEN_TTL = raw;
    return (configuration() as { jwt: { accessTtlSec: number } }).jwt.accessTtlSec;
  }

  it("parses '15m' to 900 seconds", () => expect(ttl('15m')).toBe(900));
  it("parses '30s' to 30 seconds", () => expect(ttl('30s')).toBe(30));
  it("parses '2h' to 7200 seconds", () => expect(ttl('2h')).toBe(7200));
  it("parses '7d' to 604800 seconds", () => expect(ttl('7d')).toBe(604800));
  it('treats plain numbers as seconds', () => expect(ttl('60')).toBe(60));
  it('falls back on invalid input', () => expect(ttl('soon')).toBe(900));
});

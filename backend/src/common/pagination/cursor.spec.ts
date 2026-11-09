import {
  decodeCursor,
  encodeCursor,
  parsePage,
  toPage,
} from './cursor';

describe('cursor pagination', () => {
  it('encodes and decodes a cursor round-trip', () => {
    const d = new Date('2026-01-01T00:00:00.000Z');
    const c = encodeCursor(d, 'abc-123');
    expect(decodeCursor(c)).toEqual({ createdAt: d, id: 'abc-123' });
  });

  it('rejects malformed cursors', () => {
    expect(() => decodeCursor('!!!not-base64!!!')).toThrow();
    expect(() =>
      decodeCursor(Buffer.from('{"nope":1}').toString('base64url')),
    ).toThrow();
  });

  it('parses limit with defaults and bounds', () => {
    expect(parsePage({})).toEqual({ limit: 20, cursor: null });
    expect(parsePage({ limit: '5' }).limit).toBe(5);
    expect(() => parsePage({ limit: '0' })).toThrow();
    expect(() => parsePage({ limit: '101' })).toThrow();
    expect(() => parsePage({ limit: 'abc' })).toThrow();
  });

  it('slices pages with nextCursor only when more rows exist', () => {
    const rows = [1, 2, 3].map((i) => ({
      id: `id-${i}`,
      createdAt: new Date(`2026-01-0${i}T00:00:00.000Z`),
    }));
    const full = toPage(rows, 2);
    expect(full.items).toHaveLength(2);
    expect(full.nextCursor).toBeTruthy();
    const last = toPage(rows.slice(0, 2), 5);
    expect(last.items).toHaveLength(2);
    expect(last.nextCursor).toBeNull();
  });
});

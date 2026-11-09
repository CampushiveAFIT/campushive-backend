import { FeedRankingService } from './feed-ranking.service';

describe('FeedRankingService', () => {
  const svc = new FeedRankingService();
  const HOUR = 3_600_000;

  it('scores posts by recency plus engagement weights', () => {
    const t = new Date('2026-01-01T00:00:00.000Z');
    const base = t.getTime() / HOUR;
    expect(svc.scorePost(t, 0, 0)).toBe(base);
    expect(svc.scorePost(t, 1, 0)).toBe(base + 2);
    expect(svc.scorePost(t, 0, 1)).toBe(base + 3);
    expect(svc.scoreEvent(t, 5)).toBe(base + 5);
  });

  it('sorts by score desc, then recency, then id deterministically', () => {
    const t0 = new Date('2026-01-01T00:00:00.000Z');
    const t1 = new Date('2026-01-01T01:00:00.000Z');
    const ranked = svc.rank([
      { type: 'POST', id: 'b', createdAt: t1, score: 10 },
      { type: 'POST', id: 'a', createdAt: t0, score: 10 },
      { type: 'EVENT', id: 'c', createdAt: t1, score: 99 },
    ]);
    expect(ranked.map((r) => r.id)).toEqual(['c', 'b', 'a']);
  });

  it('round-trips cursors and rejects garbage', () => {
    const c = svc.encodeCursor({
      score: 12.5,
      createdAt: new Date('2026-01-01T00:00:00.000Z'),
      id: 'x',
    });
    expect(svc.decodeCursor(c)).toEqual({
      score: 12.5,
      createdAt: '2026-01-01T00:00:00.000Z',
      id: 'x',
    });
    expect(() => svc.decodeCursor('nope')).toThrow();
    expect(() => svc.decodeCursor('')).toThrow();
  });

  it('paginates ranked lists with keyset cursors', () => {
    const mk = (id: string, score: number) => ({
      type: 'POST' as const,
      id,
      createdAt: new Date('2026-01-01T00:00:00.000Z'),
      score,
    });
    const ranked = svc.rank([mk('a', 3), mk('b', 2), mk('c', 1)]);
    const p1 = svc.paginate(ranked, 2, null);
    expect(p1.items.map((i) => i.id)).toEqual(['a', 'b']);
    expect(p1.nextCursor).toBeTruthy();
    const p2 = svc.paginate(ranked, 2, svc.decodeCursor(p1.nextCursor as string));
    expect(p2.items.map((i) => i.id)).toEqual(['c']);
    expect(p2.nextCursor).toBeNull();
  });
});

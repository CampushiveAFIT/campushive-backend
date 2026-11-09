import { BadRequestException, Injectable } from '@nestjs/common';
import { ApiErrorCode } from '../../../common/types/api-response';
import { FeedItem } from '../types/feed.types';

export interface RankedItem {
  type: 'POST' | 'EVENT';
  id: string;
  createdAt: Date;
  score: number;
}

export interface FeedCursor {
  score: number;
  createdAt: string;
  id: string;
}

/**
 * Ranking: deterministic and explainable (no ML).
 *
 *   score(post)  = hoursSinceEpoch(createdAt) + 2 * likeCount + 3 * commentCount
 *   score(event) = hoursSinceEpoch(createdAt) + 1 * registeredCount
 *
 * Recency dominates (one engagement point ≈ one hour of freshness); engagement
 * breaks near-ties. Sort: score DESC, createdAt DESC, id DESC.
 *
 * Pagination uses a keyset over (score, createdAt, id). Scores drift as
 * engagement changes, so pages are consistent within a bounded candidate
 * window rather than across an unbounded stream — documented tradeoff.
 */
@Injectable()
export class FeedRankingService {
  scorePost(createdAt: Date, likeCount: number, commentCount: number): number {
    return createdAt.getTime() / 3_600_000 + likeCount * 2 + commentCount * 3;
  }

  scoreEvent(createdAt: Date, registeredCount: number): number {
    return createdAt.getTime() / 3_600_000 + registeredCount;
  }

  rank<T extends RankedItem>(items: T[]): T[] {
    return [...items].sort(
      (a, b) =>
        b.score - a.score ||
        b.createdAt.getTime() - a.createdAt.getTime() ||
        (a.id < b.id ? 1 : a.id > b.id ? -1 : 0),
    );
  }

  encodeCursor(item: { score: number; createdAt: Date; id: string }): string {
    return Buffer.from(
      JSON.stringify({
        score: item.score,
        createdAt: item.createdAt.toISOString(),
        id: item.id,
      }),
      'utf8',
    ).toString('base64url');
  }

  decodeCursor(raw: string): FeedCursor {
    try {
      const parsed = JSON.parse(
        Buffer.from(raw, 'base64url').toString('utf8'),
      ) as { score?: unknown; createdAt?: unknown; id?: unknown };
      if (
        typeof parsed.score !== 'number' ||
        !Number.isFinite(parsed.score) ||
        typeof parsed.id !== 'string' ||
        typeof parsed.createdAt !== 'string' ||
        Number.isNaN(Date.parse(parsed.createdAt))
      ) {
        throw new Error('malformed feed cursor');
      }
      return {
        score: parsed.score,
        createdAt: parsed.createdAt,
        id: parsed.id,
      };
    } catch {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Invalid feed cursor.',
      });
    }
  }

  /** Keyset slice of an already-ranked list. */
  paginate<T extends RankedItem>(
    ranked: T[],
    limit: number,
    cursor: FeedCursor | null,
  ): { items: T[]; nextCursor: string | null } {
    const after = cursor
      ? ranked.filter(
          (i) =>
            i.score < cursor.score ||
            (i.score === cursor.score &&
              (i.createdAt.toISOString() < cursor.createdAt ||
                (i.createdAt.toISOString() === cursor.createdAt &&
                  i.id < cursor.id))),
        )
      : ranked;
    const items = after.slice(0, limit);
    const last = items[items.length - 1];
    return {
      items,
      nextCursor:
        after.length > limit && last
          ? this.encodeCursor({
              score: (last as { score: number }).score,
              createdAt: last.createdAt,
              id: last.id,
            })
          : null,
    };
  }

  toFeedItems(
    ranked: Array<RankedItem & { post?: FeedItem['post']; event?: FeedItem['event'] }>,
  ): FeedItem[] {
    return ranked.map((r) => ({
      type: r.type,
      id: r.id,
      createdAt: r.createdAt,
      score: r.score,
      ...(r.type === 'POST' ? { post: r.post } : { event: r.event }),
    }));
  }
}

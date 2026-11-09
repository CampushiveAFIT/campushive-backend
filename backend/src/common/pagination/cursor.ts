import { BadRequestException } from '@nestjs/common';
import { ApiErrorCode } from '../types/api-response';

export interface ParsedPage {
  limit: number;
  cursor: { createdAt: Date; id: string } | null;
}

export interface PageResult<T> {
  items: T[];
  nextCursor: string | null;
}

const DEFAULT_LIMIT = 20;
const MAX_LIMIT = 100;

/** Decodes an opaque cursor produced by encodeCursor. */
export function decodeCursor(raw: string): { createdAt: Date; id: string } {
  try {
    const json = Buffer.from(raw, 'base64url').toString('utf8');
    const parsed = JSON.parse(json) as { createdAt?: unknown; id?: unknown };
    if (
      typeof parsed.id !== 'string' ||
      typeof parsed.createdAt !== 'string' ||
      Number.isNaN(Date.parse(parsed.createdAt))
    ) {
      throw new Error('malformed cursor');
    }
    return { createdAt: new Date(parsed.createdAt), id: parsed.id };
  } catch {
    throw new BadRequestException({
      code: ApiErrorCode.VALIDATION_ERROR,
      message: 'Invalid pagination cursor.',
    });
  }
}

export function encodeCursor(createdAt: Date, id: string): string {
  return Buffer.from(
    JSON.stringify({ createdAt: createdAt.toISOString(), id }),
    'utf8',
  ).toString('base64url');
}

/** Parses ?limit=&cursor= query input. */
export function parsePage(query: { limit?: unknown; cursor?: unknown }): ParsedPage {
  let limit = DEFAULT_LIMIT;
  if (query.limit !== undefined) {
    const n = typeof query.limit === 'string' ? Number(query.limit) : query.limit;
    if (!Number.isInteger(n) || (n as number) < 1 || (n as number) > MAX_LIMIT) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: `limit must be an integer between 1 and ${MAX_LIMIT}.`,
      });
    }
    limit = n as number;
  }
  let cursor: ParsedPage['cursor'] = null;
  if (query.cursor !== undefined && query.cursor !== null && query.cursor !== '') {
    if (typeof query.cursor !== 'string') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Invalid pagination cursor.',
      });
    }
    cursor = decodeCursor(query.cursor);
  }
  return { limit, cursor };
}

/**
 * Slices limit+1 fetched rows (ordered newest-first) into a page.
 * Items must carry createdAt + id.
 */
export function toPage<T extends { createdAt: Date; id: string }>(
  rows: T[],
  limit: number,
): PageResult<T> {
  const items = rows.slice(0, limit);
  const nextCursor =
    rows.length > limit
      ? encodeCursor(items[items.length - 1].createdAt, items[items.length - 1].id)
      : null;
  return { items, nextCursor };
}

/**
 * Keyset fragment for (createdAt DESC, id DESC) pagination.
 * Caller spreads the result into a Prisma where clause.
 */
export function olderThan(createdAt: Date, id: string): {
  OR: Array<{ createdAt: { lt: Date } } | { createdAt: Date; id: { lt: string } }>;
} {
  return {
    OR: [{ createdAt: { lt: createdAt } }, { createdAt, id: { lt: id } }],
  };
}

/**
 * Keyset fragment for (createdAt ASC, id ASC) pagination (e.g. comments).
 */
export function newerThan(createdAt: Date, id: string): {
  OR: Array<{ createdAt: { gt: Date } } | { createdAt: Date; id: { gt: string } }>;
} {
  return {
    OR: [{ createdAt: { gt: createdAt } }, { createdAt, id: { gt: id } }],
  };
}

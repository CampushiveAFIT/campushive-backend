/**
 * Transactional outbox event types. Emitters persist one of these atomically
 * with the originating business write; the processor routes them to handlers.
 */
import { Prisma } from '@prisma/client';

export const DomainEventTypes = {
  POST_LIKED: 'post.liked',
  POST_COMMENTED: 'post.commented',
  USER_FOLLOWED: 'user.followed',
  EVENT_CANCELLED: 'event.cancelled',
  MESSAGE_CREATED: 'message.created',
  MESSAGE_UPDATED: 'message.updated',
  MESSAGE_DELETED: 'message.deleted',
} as const;

export type DomainEventType =
  (typeof DomainEventTypes)[keyof typeof DomainEventTypes];

export interface OutboxPayload {
  type: string;
  aggregateType: string;
  aggregateId: string;
  actorId: string | null;
  payload: Prisma.InputJsonValue;
}

/** Pure builder so emitters share one envelope shape (unit-testable). */
export function buildOutboxEvent(
  type: DomainEventType,
  aggregateType: string,
  aggregateId: string,
  actorId: string | null,
  payload: Record<string, string>,
): OutboxPayload {
  return { type, aggregateType, aggregateId, actorId, payload };
}

/** Backoff for retries: 30s × attempts, capped at 10 minutes. */
export function retryDelayMs(attempts: number): number {
  return Math.min(30_000 * Math.max(1, attempts), 600_000);
}

export const OUTBOX_MAX_ATTEMPTS = 10;
export const OUTBOX_BATCH_SIZE = 50;

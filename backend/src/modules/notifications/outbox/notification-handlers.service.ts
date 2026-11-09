import { Injectable, Logger } from '@nestjs/common';
import { NotificationType, Prisma } from '@prisma/client';
import { PrismaService } from '../../../config/prisma.service';
import { NotificationsGateway } from '../realtime/notifications.gateway';

export interface NotificationView {
  id: string;
  type: string;
  title: string;
  body: string | null;
  data: unknown;
  actor: { id: string; displayName: string | null; avatarUrl: string | null } | null;
  isRead: boolean;
  readAt: Date | null;
  createdAt: Date;
}

type Handler = (event: {
  id: string;
  actorId: string | null;
  payload: Record<string, unknown>;
}) => Promise<void>;

/**
 * Domain-event handlers: resolve recipients, honor preferences, create
 * notifications idempotently (dedupeKey), then emit realtime delivery.
 * Every handler is safe to re-run: dedupeKey uniqueness makes creation
 * at-most-once per originating action.
 */
@Injectable()
export class NotificationHandlers {
  private readonly logger = new Logger(NotificationHandlers.name);

  constructor(
    private readonly prisma: PrismaService,
    private readonly gateway: NotificationsGateway,
  ) {}

  async dispatch(type: string, event: { id: string; actorId: string | null; payload: Prisma.JsonValue }): Promise<void> {
    const payload = (event.payload ?? {}) as Record<string, unknown>;
    const handlers: Record<string, Handler> = {
      'post.liked': (e) => this.onPostLiked(e),
      'post.commented': (e) => this.onPostCommented(e),
      'user.followed': (e) => this.onUserFollowed(e),
      'event.cancelled': (e) => this.onEventCancelled(e),
    };
    const handler = handlers[type];
    if (!handler) {
      this.logger.warn(`no handler for domain event type "${type}"`);
      return;
    }
    await handler({ id: event.id, actorId: event.actorId, payload });
  }

  // --------------------------------------------------------------- helpers
  private async actorName(actorId: string | null): Promise<{ id: string | null; name: string }> {
    if (!actorId) return { id: null, name: 'Someone' };
    const actor = await this.prisma.user.findUnique({
      where: { id: actorId },
      select: { id: true, displayName: true, email: true },
    });
    if (!actor) return { id: null, name: 'Someone' };
    return { id: actor.id, name: actor.displayName?.trim() ? actor.displayName : actor.email };
  }

  private async prefs(userId: string): Promise<{
    likesEnabled: boolean;
    commentsEnabled: boolean;
    followsEnabled: boolean;
    eventUpdatesEnabled: boolean;
  }> {
    const row = await this.prisma.notificationPreference.findUnique({
      where: { userId },
    });
    return {
      likesEnabled: row?.likesEnabled ?? true,
      commentsEnabled: row?.commentsEnabled ?? true,
      followsEnabled: row?.followsEnabled ?? true,
      eventUpdatesEnabled: row?.eventUpdatesEnabled ?? true,
    };
  }

  private async store(input: {
    recipientId: string;
    actorId: string | null;
    type: NotificationType;
    title: string;
    body?: string;
    data?: Record<string, unknown>;
    dedupeKey: string;
  }): Promise<void> {
    try {
      const created = await this.prisma.notification.create({
        data: {
          recipientId: input.recipientId,
          actorId: input.actorId,
          type: input.type,
          title: input.title,
          body: input.body ?? null,
          data: (input.data ?? {}) as Prisma.InputJsonValue,
          dedupeKey: input.dedupeKey,
        },
        include: {
          actor: { select: { id: true, displayName: true, avatarUrl: true } },
        },
      });
      this.gateway.emitToUser(input.recipientId, this.toView(created));
      return;
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        // Already created by an earlier delivery attempt: idempotent no-op.
        return;
      }
      throw e;
    }
  }

  toView(n: {
    id: string;
    type: string;
    title: string;
    body: string | null;
    data: unknown;
    actor: { id: string; displayName: string | null; avatarUrl: string | null } | null;
    isRead: boolean;
    readAt: Date | null;
    createdAt: Date;
  }): NotificationView {
    return {
      id: n.id,
      type: n.type,
      title: n.title,
      body: n.body,
      data: n.data,
      actor: n.actor,
      isRead: n.isRead,
      readAt: n.readAt,
      createdAt: n.createdAt,
    };
  }

  // --------------------------------------------------------------- handlers
  private async onPostLiked(e: { actorId: string | null; payload: Record<string, unknown> }): Promise<void> {
    const postId = e.payload.postId as string;
    const likeId = e.payload.likeId as string;
    const post = await this.prisma.post.findUnique({
      where: { id: postId },
      select: { authorId: true, deletedAt: true },
    });
    if (!post || post.deletedAt || post.authorId === e.actorId) return;
    if (!(await this.prefs(post.authorId)).likesEnabled) return;
    const actor = await this.actorName(e.actorId);
    await this.store({
      recipientId: post.authorId,
      actorId: actor.id,
      type: 'LIKE',
      title: `${actor.name} liked your post`,
      data: { postId },
      dedupeKey: `like:${likeId}`,
    });
  }

  private async onPostCommented(e: { actorId: string | null; payload: Record<string, unknown> }): Promise<void> {
    const postId = e.payload.postId as string;
    const commentId = e.payload.commentId as string;
    const [post, comment] = await Promise.all([
      this.prisma.post.findUnique({
        where: { id: postId },
        select: { authorId: true, deletedAt: true },
      }),
      this.prisma.comment.findUnique({
        where: { id: commentId },
        select: { content: true, deletedAt: true },
      }),
    ]);
    if (!post || post.deletedAt || post.authorId === e.actorId) return;
    if (!comment || comment.deletedAt) return;
    if (!(await this.prefs(post.authorId)).commentsEnabled) return;
    const actor = await this.actorName(e.actorId);
    const excerpt =
      comment.content.length > 140 ? `${comment.content.slice(0, 140)}…` : comment.content;
    await this.store({
      recipientId: post.authorId,
      actorId: actor.id,
      type: 'COMMENT',
      title: `${actor.name} commented on your post`,
      body: excerpt,
      data: { postId, commentId },
      dedupeKey: `comment:${commentId}`,
    });
  }

  private async onUserFollowed(e: { actorId: string | null; payload: Record<string, unknown> }): Promise<void> {
    const followerId = e.payload.followerId as string;
    const followingId = e.payload.followingId as string;
    const followId = e.payload.followId as string;
    if (!(await this.prefs(followingId)).followsEnabled) return;
    // Skip if the follow was undone before processing.
    const still = await this.prisma.follow.findUnique({
      where: { followerId_followingId: { followerId, followingId } },
      select: { id: true },
    });
    if (!still) return;
    const actor = await this.actorName(followerId);
    await this.store({
      recipientId: followingId,
      actorId: actor.id,
      type: 'FOLLOW',
      title: `${actor.name} started following you`,
      data: { followerId },
      dedupeKey: `follow:${followId}`,
    });
  }

  private async onEventCancelled(e: { payload: Record<string, unknown> }): Promise<void> {
    const eventId = e.payload.eventId as string;
    const title = e.payload.title as string;
    const event = await this.prisma.event.findUnique({
      where: { id: eventId },
      select: { id: true, status: true },
    });
    // Only notify while the cancellation stands.
    if (!event || event.status !== 'CANCELLED') return;
    const holders = await this.prisma.ticket.findMany({
      where: { eventId, status: 'ACTIVE' },
      select: { userId: true },
      take: 1000,
    });
    for (const h of holders) {
      if (!(await this.prefs(h.userId)).eventUpdatesEnabled) continue;
      await this.store({
        recipientId: h.userId,
        actorId: null,
        type: 'EVENT_CANCELLED',
        title: `Event cancelled: ${title}`,
        body: 'An event you registered for was cancelled by the organizer.',
        data: { eventId },
        dedupeKey: `event-cancelled:${eventId}:${h.userId}`,
      });
    }
  }
}

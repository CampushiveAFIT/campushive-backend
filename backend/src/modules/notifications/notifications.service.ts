import { Injectable, NotFoundException } from '@nestjs/common';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  ParsedPage,
  olderThan,
  toPage,
  PageResult,
} from '../../common/pagination/cursor';
import { NotificationHandlers, NotificationView } from './outbox/notification-handlers.service';

export interface PreferenceView {
  likesEnabled: boolean;
  commentsEnabled: boolean;
  followsEnabled: boolean;
  eventUpdatesEnabled: boolean;
  updatedAt: Date | null;
}

const DEFAULT_PREFS: PreferenceView = {
  likesEnabled: true,
  commentsEnabled: true,
  followsEnabled: true,
  eventUpdatesEnabled: true,
  updatedAt: null,
};

@Injectable()
export class NotificationsService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly handlers: NotificationHandlers,
  ) {}

  private toView(n: {
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
    return this.handlers.toView(n);
  }

  async list(
    viewerId: string,
    page: ParsedPage,
    unreadOnly: boolean,
  ): Promise<PageResult<NotificationView>> {
    const where = unreadOnly
      ? { recipientId: viewerId, isRead: false }
      : { recipientId: viewerId };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.notification.findMany({
      where: cursorWhere,
      include: {
        actor: { select: { id: true, displayName: true, avatarUrl: true } },
      },
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(rows, page.limit);
    return {
      items: result.items.map((n) => this.toView(n)),
      nextCursor: result.nextCursor,
    };
  }

  async unreadCount(viewerId: string): Promise<{ unread: number }> {
    const unread = await this.prisma.notification.count({
      where: { recipientId: viewerId, isRead: false },
    });
    return { unread };
  }

  /** Owner-only, idempotent. Unknown-or-foreign IDs are 404 (no leak). */
  async markRead(viewerId: string, id: string): Promise<NotificationView> {
    const n = await this.prisma.notification.findUnique({
      where: { id },
      include: {
        actor: { select: { id: true, displayName: true, avatarUrl: true } },
      },
    });
    if (!n || n.recipientId !== viewerId) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Notification not found.',
      });
    }
    if (!n.isRead) {
      const updated = await this.prisma.notification.update({
        where: { id },
        data: { isRead: true, readAt: new Date() },
        include: {
          actor: { select: { id: true, displayName: true, avatarUrl: true } },
        },
      });
      return this.toView(updated);
    }
    return this.toView(n);
  }

  async markAllRead(viewerId: string): Promise<{ marked: number }> {
    const res = await this.prisma.notification.updateMany({
      where: { recipientId: viewerId, isRead: false },
      data: { isRead: true, readAt: new Date() },
    });
    return { marked: res.count };
  }

  async getPreferences(viewerId: string): Promise<PreferenceView> {
    const row = await this.prisma.notificationPreference.findUnique({
      where: { userId: viewerId },
    });
    if (!row) return { ...DEFAULT_PREFS };
    return {
      likesEnabled: row.likesEnabled,
      commentsEnabled: row.commentsEnabled,
      followsEnabled: row.followsEnabled,
      eventUpdatesEnabled: row.eventUpdatesEnabled,
      updatedAt: row.updatedAt,
    };
  }

  async updatePreferences(
    viewerId: string,
    dto: Partial<Record<keyof Omit<PreferenceView, 'updatedAt'>, boolean>>,
  ): Promise<PreferenceView> {
    const data: Record<string, boolean> = {};
    for (const key of [
      'likesEnabled',
      'commentsEnabled',
      'followsEnabled',
      'eventUpdatesEnabled',
    ] as const) {
      if (dto[key] !== undefined) data[key] = dto[key] as boolean;
    }
    const row = await this.prisma.notificationPreference.upsert({
      where: { userId: viewerId },
      create: { userId: viewerId, ...data },
      update: data,
    });
    return {
      likesEnabled: row.likesEnabled,
      commentsEnabled: row.commentsEnabled,
      followsEnabled: row.followsEnabled,
      eventUpdatesEnabled: row.eventUpdatesEnabled,
      updatedAt: row.updatedAt,
    };
  }
}

import {
  BadRequestException,
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  DomainEventTypes,
  buildOutboxEvent,
} from '../notifications/outbox/domain-event';
import {
  ParsedPage,
  olderThan,
  toPage,
  PageResult,
} from '../../common/pagination/cursor';

export interface GraphUser {
  id: string;
  displayName: string | null;
  avatarUrl: string | null;
}

@Injectable()
export class UserGraphService {
  constructor(private readonly prisma: PrismaService) {}

  private async activeUserOrThrow(id: string) {
    const u = await this.prisma.user.findUnique({ where: { id } });
    if (!u || !u.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'User not found.',
      });
    }
    return u;
  }

  private async blockedEitherWay(a: string, b: string): Promise<boolean> {
    if (a === b) return false;
    const found = await this.prisma.block.findFirst({
      where: {
        OR: [
          { blockerId: a, blockedId: b },
          { blockerId: b, blockedId: a },
        ],
      },
      select: { id: true },
    });
    return found !== null;
  }

  // ------------------------------------------------------------------ follow
  async follow(viewerId: string, targetId: string) {
    if (viewerId === targetId) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'You cannot follow yourself.',
      });
    }
    await this.activeUserOrThrow(targetId);
    if (await this.blockedEitherWay(viewerId, targetId)) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'You cannot follow this user.',
      });
    }
    try {
      // Outbox persisted atomically with the follow itself.
      await this.prisma.$transaction(async (tx) => {
        const follow = await tx.follow.create({
          data: { followerId: viewerId, followingId: targetId },
        });
        await tx.domainEvent.create({
          data: buildOutboxEvent(
            DomainEventTypes.USER_FOLLOWED,
            'Follow',
            follow.id,
            viewerId,
            { followerId: viewerId, followingId: targetId, followId: follow.id },
          ),
        });
      });
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        return { following: true };
      }
      throw e;
    }
    return { following: true };
  }

  async unfollow(viewerId: string, targetId: string) {
    await this.prisma.follow.deleteMany({
      where: { followerId: viewerId, followingId: targetId },
    });
    return { following: false };
  }

  async followers(
    viewerId: string,
    targetId: string,
    page: ParsedPage,
  ): Promise<PageResult<{ user: GraphUser; createdAt: Date }>> {
    const target = await this.activeUserOrThrow(targetId);
    void target;
    // Blocked users (either direction) never appear in others' lists.
    const blocked = viewerId === targetId ? [] : [...(await this.blockedUserIds(viewerId))];
    const where = {
      followingId: targetId,
      followerId: { notIn: blocked },
    };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.follow.findMany({
      where: cursorWhere,
      include: {
        follower: { select: { id: true, displayName: true, avatarUrl: true } },
      },
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(
      rows.map((r) => ({ id: r.id, createdAt: r.createdAt, user: r.follower })),
      page.limit,
    );
    return {
      items: result.items.map((i) => ({ user: i.user, createdAt: i.createdAt })),
      nextCursor: result.nextCursor,
    };
  }

  async following(
    viewerId: string,
    targetId: string,
    page: ParsedPage,
  ): Promise<PageResult<{ user: GraphUser; createdAt: Date }>> {
    const target = await this.activeUserOrThrow(targetId);
    void target;
    const blocked = viewerId === targetId ? [] : [...(await this.blockedUserIds(viewerId))];
    const where = {
      followerId: targetId,
      followingId: { notIn: blocked },
    };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.follow.findMany({
      where: cursorWhere,
      include: {
        following: { select: { id: true, displayName: true, avatarUrl: true } },
      },
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(
      rows.map((r) => ({
        id: r.id,
        createdAt: r.createdAt,
        user: r.following,
      })),
      page.limit,
    );
    return {
      items: result.items.map((i) => ({ user: i.user, createdAt: i.createdAt })),
      nextCursor: result.nextCursor,
    };
  }

  private async blockedUserIds(userId: string): Promise<Set<string>> {
    const [given, received] = await Promise.all([
      this.prisma.block.findMany({
        where: { blockerId: userId },
        select: { blockedId: true },
      }),
      this.prisma.block.findMany({
        where: { blockedId: userId },
        select: { blockerId: true },
      }),
    ]);
    const set = new Set<string>();
    for (const b of given) set.add(b.blockedId);
    for (const b of received) set.add(b.blockerId);
    return set;
  }

  // ------------------------------------------------------------------- block
  async block(viewerId: string, targetId: string) {
    if (viewerId === targetId) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'You cannot block yourself.',
      });
    }
    await this.activeUserOrThrow(targetId);
    const existing = await this.prisma.block.findUnique({
      where: { blockerId_blockedId: { blockerId: viewerId, blockedId: targetId } },
    });
    if (existing) return { blocked: true };
    // Blocking severs follow ties in both directions so follow-based
    // visibility can never leak across a block.
    await this.prisma.$transaction([
      this.prisma.block.create({
        data: { blockerId: viewerId, blockedId: targetId },
      }),
      this.prisma.follow.deleteMany({
        where: {
          OR: [
            { followerId: viewerId, followingId: targetId },
            { followerId: targetId, followingId: viewerId },
          ],
        },
      }),
    ]);
    return { blocked: true };
  }

  async unblock(viewerId: string, targetId: string) {
    await this.prisma.block.deleteMany({
      where: { blockerId: viewerId, blockedId: targetId },
    });
    return { blocked: false };
  }
}

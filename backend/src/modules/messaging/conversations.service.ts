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
  ParsedPage,
  encodeCursor,
  PageResult,
} from '../../common/pagination/cursor';
import { directKey } from './direct-key';
import {
  ConversationPeerView,
  ConversationView,
  toLastMessageView,
} from './views';

@Injectable()
export class ConversationsService {
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

  /** Membership or 404 (unknown conversation and non-membership are identical). */
  async membershipOrThrow(viewerId: string, conversationId: string) {
    const member = await this.prisma.conversationMember.findUnique({
      where: {
        conversationId_userId: { conversationId, userId: viewerId },
      },
      include: {
        conversation: { select: { id: true } },
      },
    });
    if (!member) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Conversation not found.',
      });
    }
    return member;
  }

  async otherMember(
    conversationId: string,
    viewerId: string,
  ): Promise<ConversationPeerView> {
    const other = await this.prisma.conversationMember.findFirst({
      where: { conversationId, userId: { not: viewerId } },
      include: {
        user: { select: { id: true, displayName: true, avatarUrl: true } },
      },
    });
    if (!other) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Conversation is no longer available.',
      });
    }
    return other.user;
  }

  async project(
    viewerId: string,
    conversationId: string,
  ): Promise<ConversationView> {
    const member = await this.membershipOrThrow(viewerId, conversationId);
    const [peer, conversation] = await Promise.all([
      this.otherMember(conversationId, viewerId),
      this.prisma.conversation.findUniqueOrThrow({
        where: { id: conversationId },
      }),
    ]);
    const stored = conversation.lastMessageId
      ? await this.prisma.message.findUnique({
          where: { id: conversation.lastMessageId },
        })
      : null;
    return {
      id: conversation.id,
      type: conversation.type,
      participant: peer,
      lastMessage: stored ? toLastMessageView(stored) : null,
      lastMessageAt: conversation.lastMessageAt,
      unreadCount: member.unreadCount,
      createdAt: conversation.createdAt,
    };
  }

  async getOrCreateDirect(
    viewerId: string,
    targetId: string,
  ): Promise<{ conversation: ConversationView; created: boolean }> {
    if (viewerId === targetId) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'You cannot message yourself.',
      });
    }
    await this.activeUserOrThrow(targetId);
    if (await this.blockedEitherWay(viewerId, targetId)) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'You cannot message this user.',
      });
    }
    const key = directKey(viewerId, targetId);
    const existing = await this.prisma.conversation.findUnique({
      where: { directKey: key },
      select: { id: true },
    });
    if (existing) {
      return { conversation: await this.project(viewerId, existing.id), created: false };
    }
    try {
      const created = await this.prisma.$transaction(async (tx) => {
        // Re-check the block inside the transaction: a block created
        // concurrently must not be bypassed by conversation creation.
        const blocked = await tx.block.findFirst({
          where: {
            OR: [
              { blockerId: viewerId, blockedId: targetId },
              { blockerId: targetId, blockedId: viewerId },
            ],
          },
          select: { id: true },
        });
        if (blocked) {
          throw new ForbiddenException({
            code: ApiErrorCode.FORBIDDEN,
            message: 'You cannot message this user.',
          });
        }
        const conversation = await tx.conversation.create({
          data: { type: 'DIRECT', directKey: key },
        });
        await tx.conversationMember.createMany({
          data: [
            { conversationId: conversation.id, userId: viewerId },
            { conversationId: conversation.id, userId: targetId },
          ],
        });
        return conversation;
      });
      return { conversation: await this.project(viewerId, created.id), created: true };
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        // Lost a concurrent creation race: return the winner idempotently.
        const winner = await this.prisma.conversation.findUniqueOrThrow({
          where: { directKey: key },
          select: { id: true },
        });
        return { conversation: await this.project(viewerId, winner.id), created: false };
      }
      throw e;
    }
  }

  async list(
    viewerId: string,
    page: ParsedPage,
  ): Promise<PageResult<ConversationView>> {
    const cursor = page.cursor;
    // Keyset over (lastMessageAt DESC NULLS LAST, conversationId DESC).
    // A null lastMessageAt cursor means "continue past empty conversations".
    const cursorFilter = !cursor
      ? {}
      : cursor.createdAt.getTime() === 0
        ? { conversation: { lastMessageAt: null, id: { lt: cursor.id } } }
        : {
            conversation: {
              OR: [
                { lastMessageAt: { lt: cursor.createdAt } },
                { lastMessageAt: cursor.createdAt, id: { lt: cursor.id } },
                { lastMessageAt: null },
              ],
            },
          };
    // One query for the page of memberships; peers and last messages are
    // then fetched in two batched queries (no per-row N+1).
    const memberships = await this.prisma.conversationMember.findMany({
      where: { userId: viewerId, ...cursorFilter },
      include: {
        conversation: {
          select: {
            id: true,
            type: true,
            createdAt: true,
            lastMessageAt: true,
            lastMessageId: true,
          },
        },
      },
      orderBy: [
        { conversation: { lastMessageAt: { sort: 'desc', nulls: 'last' } } },
        { conversationId: 'desc' },
      ],
      take: page.limit + 1,
    });
    const pageRows = memberships.slice(0, page.limit);
    const ids = pageRows.map((m) => m.conversationId);
    const [peers, lastMessages] = await Promise.all([
      ids.length === 0
        ? []
        : this.prisma.conversationMember.findMany({
            where: { conversationId: { in: ids }, userId: { not: viewerId } },
            include: {
              user: { select: { id: true, displayName: true, avatarUrl: true } },
            },
          }),
      (() => {
        const messageIds = pageRows
          .map((m) => m.conversation.lastMessageId)
          .filter((v): v is string => v !== null);
        return messageIds.length === 0
          ? Promise.resolve([])
          : this.prisma.message.findMany({
              where: { id: { in: messageIds } },
            });
      })(),
    ]);
    const peerByConversation = new Map<string, ConversationPeerView>();
    for (const p of peers) peerByConversation.set(p.conversationId, p.user);
    const messageById = new Map(lastMessages.map((m) => [m.id, m]));
    const items: ConversationView[] = pageRows.map((m) => {
      const peer = peerByConversation.get(m.conversationId);
      if (!peer) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'Conversation is no longer available.',
        });
      }
      const stored = m.conversation.lastMessageId
        ? (messageById.get(m.conversation.lastMessageId) ?? null)
        : null;
      return {
        id: m.conversation.id,
        type: m.conversation.type,
        participant: peer,
        lastMessage: stored ? toLastMessageView(stored) : null,
        lastMessageAt: m.conversation.lastMessageAt,
        unreadCount: m.unreadCount,
        createdAt: m.conversation.createdAt,
      };
    });
    const nextCursor =
      memberships.length > page.limit
        ? encodeCursor(
            memberships[page.limit - 1].conversation.lastMessageAt ?? new Date(0),
            memberships[page.limit - 1].conversationId,
          )
        : null;
    return { items, nextCursor };
  }

  async unreadTotal(viewerId: string): Promise<{ unread: number }> {
    const agg = await this.prisma.conversationMember.aggregate({
      where: { userId: viewerId },
      _sum: { unreadCount: true },
    });
    return { unread: agg._sum.unreadCount ?? 0 };
  }
}

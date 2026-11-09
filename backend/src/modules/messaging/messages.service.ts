import {
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  ParsedPage,
  olderThan,
  toPage,
  PageResult,
} from '../../common/pagination/cursor';
import {
  DomainEventTypes,
  buildOutboxEvent,
} from '../notifications/outbox/domain-event';
import { ConversationsService } from './conversations.service';
import { MessageView, toMessageView } from './views';
import { SendMessageDto, UpdateMessageDto } from './dto/messaging.dto';

@Injectable()
export class MessagesService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly conversations: ConversationsService,
  ) {}

  // ------------------------------------------------------------------- send
  async send(
    viewerId: string,
    conversationId: string,
    dto: SendMessageDto,
  ): Promise<{ message: MessageView; created: boolean }> {
    await this.conversations.membershipOrThrow(viewerId, conversationId);
    // Idempotent retry: same sender + clientMessageId returns the original.
    const existing = await this.prisma.message.findUnique({
      where: {
        senderId_clientMessageId: {
          senderId: viewerId,
          clientMessageId: dto.clientMessageId,
        },
      },
    });
    if (existing) {
      return { message: toMessageView(existing), created: false };
    }
    try {
      const created = await this.prisma.$transaction(async (tx) => {
        // Re-check the block inside the transaction: a block created
        // concurrently must not be bypassed by an in-flight send.
        const peer = await tx.conversationMember.findFirst({
          where: { conversationId, userId: { not: viewerId } },
          select: { userId: true },
        });
        if (!peer) {
          throw new ForbiddenException({
            code: ApiErrorCode.FORBIDDEN,
            message: 'Conversation is no longer available.',
          });
        }
        const blocked = await tx.block.findFirst({
          where: {
            OR: [
              { blockerId: viewerId, blockedId: peer.userId },
              { blockerId: peer.userId, blockedId: viewerId },
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
        const message = await tx.message.create({
          data: {
            conversationId,
            senderId: viewerId,
            clientMessageId: dto.clientMessageId,
            body: dto.body,
          },
        });
        // Outbox persisted atomically with the message itself.
        await tx.domainEvent.create({
          data: buildOutboxEvent(
            DomainEventTypes.MESSAGE_CREATED,
            'Message',
            message.id,
            viewerId,
            { messageId: message.id, conversationId },
          ),
        });
        const now = new Date();
        await tx.conversation.update({
          where: { id: conversationId },
          data: { lastMessageAt: now, lastMessageId: message.id },
        });
        await tx.conversationMember.update({
          where: {
            conversationId_userId: { conversationId, userId: peer.userId },
          },
          data: { unreadCount: { increment: 1 } },
        });
        return message;
      });
      return { message: toMessageView(created), created: true };
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        // Lost a concurrent duplicate-send race: return the winner.
        const winner = await this.prisma.message.findUniqueOrThrow({
          where: {
            senderId_clientMessageId: {
              senderId: viewerId,
              clientMessageId: dto.clientMessageId,
            },
          },
        });
        return { message: toMessageView(winner), created: false };
      }
      throw e;
    }
  }

  // ------------------------------------------------------------------- list
  async list(
    viewerId: string,
    conversationId: string,
    page: ParsedPage,
  ): Promise<PageResult<MessageView>> {
    await this.conversations.membershipOrThrow(viewerId, conversationId);
    const where = { conversationId };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.message.findMany({
      where: cursorWhere,
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(rows, page.limit);
    return {
      items: result.items.map((m) => toMessageView(m)),
      nextCursor: result.nextCursor,
    };
  }

  // ------------------------------------------------------------------ update
  async update(
    viewerId: string,
    id: string,
    dto: UpdateMessageDto,
  ): Promise<MessageView> {
    const message = await this.prisma.message.findUnique({
      where: { id },
    });
    // 404 for missing, deleted, or foreign messages: no existence probing,
    // and blocks never transfer ownership.
    if (!message || message.deletedAt || message.senderId !== viewerId) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Message not found.',
      });
    }
    await this.conversations.membershipOrThrow(
      viewerId,
      message.conversationId,
    );
    const updated = await this.prisma.$transaction(async (tx) => {
      const row = await tx.message.update({
        where: { id },
        data: { body: dto.body },
      });
      await tx.domainEvent.create({
        data: buildOutboxEvent(
          DomainEventTypes.MESSAGE_UPDATED,
          'Message',
          id,
          viewerId,
          { messageId: id, conversationId: message.conversationId },
        ),
      });
      return row;
    });
    return toMessageView(updated);
  }

  // ------------------------------------------------------------------ remove
  async remove(viewerId: string, id: string): Promise<{ deleted: boolean }> {
    const message = await this.prisma.message.findUnique({ where: { id } });
    if (!message || message.senderId !== viewerId) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Message not found.',
      });
    }
    await this.conversations.membershipOrThrow(
      viewerId,
      message.conversationId,
    );
    // Repeated deletion is idempotent: the tombstone already stands.
    if (message.deletedAt) return { deleted: true };
    await this.prisma.$transaction(async (tx) => {
      await tx.message.update({
        where: { id },
        data: { deletedAt: new Date() },
      });
      await tx.domainEvent.create({
        data: buildOutboxEvent(
          DomainEventTypes.MESSAGE_DELETED,
          'Message',
          id,
          viewerId,
          { messageId: id, conversationId: message.conversationId },
        ),
      });
      // Keep the recipient counter exact: only decrement when they had not
      // read this message yet.
      const peer = await tx.conversationMember.findFirst({
        where: {
          conversationId: message.conversationId,
          userId: { not: viewerId },
        },
      });
      if (
        peer &&
        (peer.lastReadAt === null || peer.lastReadAt < message.createdAt)
      ) {
        const current = await tx.conversationMember.findUniqueOrThrow({
          where: { id: peer.id },
          select: { unreadCount: true },
        });
        if (current.unreadCount > 0) {
          await tx.conversationMember.update({
            where: { id: peer.id },
            data: { unreadCount: { decrement: 1 } },
          });
        }
      }
    });
    return { deleted: true };
  }

  // -------------------------------------------------------------------- read
  async markRead(
    viewerId: string,
    conversationId: string,
  ): Promise<{ conversationId: string; unread: number; lastReadAt: Date }> {
    await this.conversations.membershipOrThrow(viewerId, conversationId);
    const now = new Date();
    // Conditional write: the marker only moves forward, so a concurrently
    // racing older read can never regress it.
    await this.prisma.conversationMember.updateMany({
      where: {
        conversationId,
        userId: viewerId,
        OR: [{ lastReadAt: null }, { lastReadAt: { lt: now } }],
      },
      data: { lastReadAt: now, unreadCount: 0 },
    });
    const current = await this.prisma.conversationMember.findUniqueOrThrow({
      where: {
        conversationId_userId: { conversationId, userId: viewerId },
      },
    });
    return {
      conversationId,
      unread: current.unreadCount,
      lastReadAt: current.lastReadAt ?? now,
    };
  }
}

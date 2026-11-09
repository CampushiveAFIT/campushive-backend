import { Injectable, Logger } from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import { MessagingGateway } from './realtime/messaging.gateway';
import { MessageView, toMessageView } from './views';

/**
 * Message domain-event handler: realtime delivery only (no Notification
 * rows — read state, not the inbox, tracks message unreadness). Emits to
 * both members' rooms so multi-device sync works; clients dedupe by
 * message id, which makes at-least-once redelivery safe.
 */
@Injectable()
export class MessageEventsHandler {
  private readonly logger = new Logger(MessageEventsHandler.name);

  constructor(
    private readonly prisma: PrismaService,
    private readonly gateway: MessagingGateway,
  ) {}

  async dispatch(
    type: string,
    event: { id: string; actorId: string | null; payload: Prisma.JsonValue },
  ): Promise<void> {
    if (
      type !== 'message.created' &&
      type !== 'message.updated' &&
      type !== 'message.deleted'
    ) {
      this.logger.warn(`message handler received unexpected type "${type}"`);
      return;
    }
    const payload = (event.payload ?? {}) as { messageId?: unknown };
    if (typeof payload.messageId !== 'string') {
      this.logger.warn(`message event ${event.id} missing messageId`);
      return;
    }
    const messageId = payload.messageId;
    const message = await this.prisma.message.findUnique({
      where: { id: messageId },
      include: {
        conversation: { select: { id: true, members: { select: { userId: true } } } },
      },
    });
    if (!message) return; // Message vanished; nothing to deliver.
    const view: MessageView = toMessageView(message);
    const eventName =
      type === 'message.created'
        ? 'message.created'
        : type === 'message.updated'
          ? 'message.updated'
          : 'message.deleted';
    for (const m of message.conversation.members) {
      this.gateway.emitToUser(m.userId, eventName, view);
    }
  }
}

/**
 * Pure, unit-testable response projections for 1:1 messaging.
 *
 * Nothing here touches the database. Every field is drawn from columns the
 * product contract already treats as public (user id/displayName/avatarUrl,
 * message envelope); password hashes, tokens, emails, and internal
 * membership rows never appear.
 */

export interface ConversationPeerView {
  id: string;
  displayName: string | null;
  avatarUrl: string | null;
}

export interface LastMessageView {
  id: string;
  senderId: string;
  /** Null once the message is soft-deleted (tombstone, not content). */
  body: string | null;
  createdAt: Date;
  deletedAt: Date | null;
  isDeleted: boolean;
}

export interface ConversationView {
  id: string;
  type: string;
  participant: ConversationPeerView;
  lastMessage: LastMessageView | null;
  lastMessageAt: Date | null;
  unreadCount: number;
  createdAt: Date;
}

export interface MessageView {
  id: string;
  conversationId: string;
  senderId: string;
  /** Echo of the client idempotency key, so retries can reconcile. */
  clientMessageId: string;
  /** Null once the message is soft-deleted (tombstone, not content). */
  body: string | null;
  createdAt: Date;
  updatedAt: Date;
  deletedAt: Date | null;
  isDeleted: boolean;
}

type StoredMessage = {
  id: string;
  conversationId: string;
  senderId: string;
  clientMessageId: string;
  body: string;
  createdAt: Date;
  updatedAt: Date;
  deletedAt: Date | null;
};

export function toMessageView(m: StoredMessage): MessageView {
  const deleted = m.deletedAt !== null;
  return {
    id: m.id,
    conversationId: m.conversationId,
    senderId: m.senderId,
    clientMessageId: m.clientMessageId,
    body: deleted ? null : m.body,
    createdAt: m.createdAt,
    updatedAt: m.updatedAt,
    deletedAt: m.deletedAt,
    isDeleted: deleted,
  };
}

export function toLastMessageView(m: StoredMessage): LastMessageView {
  const deleted = m.deletedAt !== null;
  return {
    id: m.id,
    senderId: m.senderId,
    body: deleted ? null : m.body,
    createdAt: m.createdAt,
    deletedAt: m.deletedAt,
    isDeleted: deleted,
  };
}

import {
  DomainEventTypes,
  buildOutboxEvent,
} from '../notifications/outbox/domain-event';

describe('message outbox events', () => {
  it('defines the message domain event types', () => {
    expect(DomainEventTypes.MESSAGE_CREATED).toBe('message.created');
    expect(DomainEventTypes.MESSAGE_UPDATED).toBe('message.updated');
    expect(DomainEventTypes.MESSAGE_DELETED).toBe('message.deleted');
  });

  it('builds a uniform envelope for message writes', () => {
    expect(
      buildOutboxEvent(
        DomainEventTypes.MESSAGE_CREATED,
        'Message',
        'msg-1',
        'user-1',
        { messageId: 'msg-1', conversationId: 'conv-1' },
      ),
    ).toEqual({
      type: 'message.created',
      aggregateType: 'Message',
      aggregateId: 'msg-1',
      actorId: 'user-1',
      payload: { messageId: 'msg-1', conversationId: 'conv-1' },
    });
  });
});

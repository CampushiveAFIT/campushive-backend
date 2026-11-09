import { toLastMessageView, toMessageView } from './views';

const base = {
  id: 'm1',
  conversationId: 'c1',
  senderId: 'u1',
  clientMessageId: 'cm1',
  body: 'Hello there',
  createdAt: new Date('2026-01-01T00:00:00.000Z'),
  updatedAt: new Date('2026-01-01T00:00:01.000Z'),
  deletedAt: null,
};

describe('messaging views', () => {
  it('projects a live message with its envelope fields', () => {
    const view = toMessageView(base);
    expect(view).toEqual({
      id: 'm1',
      conversationId: 'c1',
      senderId: 'u1',
      clientMessageId: 'cm1',
      body: 'Hello there',
      createdAt: base.createdAt,
      updatedAt: base.updatedAt,
      deletedAt: null,
      isDeleted: false,
    });
  });

  it('tombstones soft-deleted messages (body null, never leaked)', () => {
    const deletedAt = new Date('2026-01-02T00:00:00.000Z');
    const view = toMessageView({ ...base, deletedAt });
    expect(view.body).toBeNull();
    expect(view.deletedAt).toEqual(deletedAt);
    expect(view.isDeleted).toBe(true);
  });

  it('never exposes sensitive or internal fields', () => {
    const view = toMessageView(base) as unknown as Record<string, unknown>;
    for (const leaked of [
      'passwordHash',
      'email',
      'refreshToken',
      'token',
      'role',
      'sender',
    ]) {
      expect(view[leaked]).toBeUndefined();
    }
    expect(Object.keys(view).sort()).toEqual(
      [
        'body',
        'clientMessageId',
        'conversationId',
        'createdAt',
        'deletedAt',
        'id',
        'isDeleted',
        'senderId',
        'updatedAt',
      ].sort(),
    );
  });

  it('projects last-message summaries with tombstone semantics', () => {
    expect(toLastMessageView(base)).toEqual({
      id: 'm1',
      senderId: 'u1',
      body: 'Hello there',
      createdAt: base.createdAt,
      deletedAt: null,
      isDeleted: false,
    });
    const tombstone = toLastMessageView({
      ...base,
      deletedAt: new Date('2026-01-03T00:00:00.000Z'),
    });
    expect(tombstone.body).toBeNull();
    expect(tombstone.isDeleted).toBe(true);
  });
});

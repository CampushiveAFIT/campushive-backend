import { INestApplication } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import { Test, TestingModule } from '@nestjs/testing';
import { argon2id, hash } from 'argon2';
import { randomUUID } from 'crypto';
import request from 'supertest';
import { io, Socket } from 'socket.io-client';
import { AddressInfo } from 'net';
import { AppModule } from '../src/app.module';
import { PrismaService } from '../src/config/prisma.service';
import { EmailService } from '../src/common/email/email.service';
import { REDIS_CLIENT } from '../src/common/redis/redis.module';
import { OutboxProcessorService } from '../src/modules/notifications/outbox/outbox-processor.service';
import type Redis from 'ioredis';

const PASSWORD = 'Str0ng-Passw0rd!';

// Deterministic tests drive the processor explicitly; the background
// interval is parked so assertions never race it.
process.env.OUTBOX_POLL_MS = '3600000';

describe('Slice 6: 1:1 messaging + durable realtime', () => {
  let app: INestApplication;
  let prisma: PrismaService;
  let redis: Redis;
  let jwt: JwtService;
  let config: ConfigService;
  let processor: OutboxProcessorService;
  let passwordHash: string;
  let wsPort = 0;

  const emailDouble = {
    sendVerificationEmail: async () => undefined,
    sendPasswordResetEmail: async () => undefined,
  };

  beforeAll(async () => {
    const module: TestingModule = await Test.createTestingModule({
      imports: [AppModule],
    })
      .overrideProvider(EmailService)
      .useValue(emailDouble)
      .compile();

    app = module.createNestApplication();
    app.setGlobalPrefix('api/v1');
    await app.init();
    await app.listen(0);
    wsPort = (app.getHttpServer().address() as AddressInfo).port;

    prisma = app.get(PrismaService);
    redis = app.get(REDIS_CLIENT);
    jwt = app.get(JwtService);
    config = app.get(ConfigService);
    processor = app.get(OutboxProcessorService);
    passwordHash = await hash(PASSWORD, { type: argon2id });
  });

  afterAll(async () => {
    await app.close();
  });

  beforeEach(async () => {
    await prisma.message.deleteMany();
    await prisma.conversationMember.deleteMany();
    await prisma.conversation.deleteMany();
    await prisma.notification.deleteMany();
    await prisma.notificationPreference.deleteMany();
    await prisma.domainEvent.deleteMany();
    await prisma.like.deleteMany();
    await prisma.comment.deleteMany();
    await prisma.ticket.deleteMany();
    await prisma.post.deleteMany();
    await prisma.event.deleteMany();
    await prisma.follow.deleteMany();
    await prisma.block.deleteMany();
    await prisma.communityMembership.deleteMany();
    await prisma.community.deleteMany();
    await prisma.universityMembership.deleteMany();
    await prisma.university.deleteMany();
    await prisma.refreshToken.deleteMany();
    await prisma.emailVerificationToken.deleteMany();
    await prisma.passwordResetToken.deleteMany();
    await prisma.user.deleteMany();
    await redis.flushdb();
  });

  const api = () => request(app.getHttpServer());
  const auth = (token: string) => ({ Authorization: `Bearer ${token}` });
  const tick = (ms = 20) => new Promise((r) => setTimeout(r, ms));

  async function mkUser(email: string, displayName?: string) {
    const u = await prisma.user.create({
      data: { email, passwordHash, displayName: displayName ?? email.split('@')[0] },
    });
    const token = jwt.sign(
      { sub: u.id, role: 'STUDENT', jti: randomUUID() },
      { secret: config.get<string>('jwt.accessSecret'), expiresIn: 900 },
    );
    return { id: u.id, email, token };
  }

  async function direct(token: string, targetId: string, expected = 201) {
    const res = await api()
      .post('/api/v1/conversations/direct')
      .set(auth(token))
      .send({ userId: targetId })
      .expect(expected);
    return res.body.data;
  }

  async function send(
    token: string,
    convId: string,
    key: string,
    body: string,
    expected = 201,
  ) {
    const res = await api()
      .post(`/api/v1/conversations/${convId}/messages`)
      .set(auth(token))
      .send({ clientMessageId: key, body })
      .expect(expected);
    return res.body.data;
  }

  function wsClient(token?: string): Socket {
    return io(`http://127.0.0.1:${wsPort}`, {
      auth: token ? { token } : {},
      transports: ['websocket'],
      reconnection: false,
      timeout: 5000,
    });
  }

  function waitFor<T>(socket: Socket, event: string, ms = 8000): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        socket.off(event, handler);
        reject(new Error(`timeout waiting for ${event}`));
      }, ms);
      const handler = (data: T) => {
        clearTimeout(timer);
        resolve(data);
      };
      socket.once(event, handler);
    });
  }

  // ------------------------------------------------------------ happy path
  describe('conversation lifecycle (happy path)', () => {
    it('register → login → direct → send → history → read → unread 0', async () => {
      // Full HTTP auth integration (not the prisma shortcut).
      await api()
        .post('/api/v1/auth/register')
        .send({ email: 'msga@example.com', password: PASSWORD, displayName: 'Aya' })
        .expect(201);
      await api()
        .post('/api/v1/auth/register')
        .send({ email: 'msgb@example.com', password: PASSWORD, displayName: 'Ben' })
        .expect(201);
      const loginA = await api()
        .post('/api/v1/auth/login')
        .send({ email: 'msga@example.com', password: PASSWORD })
        .expect(200);
      const loginB = await api()
        .post('/api/v1/auth/login')
        .send({ email: 'msgb@example.com', password: PASSWORD })
        .expect(200);
      const tokenA = loginA.body.data.accessToken as string;
      const tokenB = loginB.body.data.accessToken as string;
      const idB = loginB.body.data.user.id as string;

      const conv = await direct(tokenA, idB, 201);
      expect(conv.type).toBe('DIRECT');
      expect(conv.participant.id).toBe(idB);
      expect(conv.participant.displayName).toBe('Ben');
      expect(conv.participant.passwordHash).toBeUndefined();
      expect(conv.lastMessage).toBeNull();
      expect(conv.unreadCount).toBe(0);

      // Idempotent second creation returns the same conversation with 200.
      const again = await direct(tokenA, idB, 200);
      expect(again.id).toBe(conv.id);

      const sent = await send(tokenA, conv.id, randomUUID(), 'Hello Ben');
      expect(sent.senderId).toBe(loginA.body.data.user.id);
      expect(sent.body).toBe('Hello Ben');
      expect(sent.isDeleted).toBe(false);

      // REST retrieval works before any outbox processing (durable first).
      const history = await api()
        .get(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(tokenB))
        .expect(200);
      expect(history.body.data.items).toHaveLength(1);
      expect(history.body.data.items[0].id).toBe(sent.id);

      const listB = await api()
        .get('/api/v1/conversations')
        .set(auth(tokenB))
        .expect(200);
      expect(listB.body.data.items).toHaveLength(1);
      expect(listB.body.data.items[0].unreadCount).toBe(1);
      expect(listB.body.data.items[0].lastMessage.id).toBe(sent.id);

      const detail = await api()
        .get(`/api/v1/conversations/${conv.id}`)
        .set(auth(tokenB))
        .expect(200);
      expect(detail.body.data.unreadCount).toBe(1);

      await api()
        .post(`/api/v1/conversations/${conv.id}/read`)
        .set(auth(tokenB))
        .expect(200);
      const unread = await api()
        .get('/api/v1/conversations/unread-count')
        .set(auth(tokenB))
        .expect(200);
      expect(unread.body.data.unread).toBe(0);
    });

    it('rejects self-conversation, unknown/malformed/inactive targets', async () => {
      const a = await mkUser('self1@example.com');
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(a.token))
        .send({ userId: a.id })
        .expect(400);
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(a.token))
        .send({ userId: '00000000-0000-4000-8000-000000000000' })
        .expect(404);
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(a.token))
        .send({ userId: 'not-a-uuid' })
        .expect(400);
      const gone = await mkUser('gone@example.com');
      await prisma.user.update({
        where: { id: gone.id },
        data: { isActive: false },
      });
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(a.token))
        .send({ userId: gone.id })
        .expect(404);
    });

    it('requires authentication on every messaging route', async () => {
      await api().get('/api/v1/conversations').expect(401);
      await api().post('/api/v1/conversations/direct').send({}).expect(401);
      await api().get('/api/v1/conversations/unread-count').expect(401);
      await api().get('/api/v1/conversations/00000000-0000-4000-8000-000000000000').expect(401);
      await api()
        .get('/api/v1/conversations/00000000-0000-4000-8000-000000000000/messages')
        .expect(401);
      await api()
        .post('/api/v1/conversations/00000000-0000-4000-8000-000000000000/messages')
        .send({})
        .expect(401);
      await api()
        .post('/api/v1/conversations/00000000-0000-4000-8000-000000000000/read')
        .expect(401);
      await api().patch('/api/v1/messages/00000000-0000-4000-8000-000000000000').send({}).expect(401);
      await api().delete('/api/v1/messages/00000000-0000-4000-8000-000000000000').expect(401);
    });
  });

  // --------------------------------------------- idempotency + outbox core
  describe('send idempotency + outbox atomicity', () => {
    it('duplicate clientMessageId returns the original (200), one row + one event', async () => {
      const a = await mkUser('idema@example.com');
      const b = await mkUser('idemb@example.com');
      const conv = await direct(a.token, b.id);
      const key = randomUUID();

      const first = await send(a.token, conv.id, key, 'once only');
      const replay = await send(a.token, conv.id, key, 'once only', 200);
      expect(replay.id).toBe(first.id);

      expect(await prisma.message.count()).toBe(1);
      const events = await prisma.domainEvent.findMany({
        where: { type: 'message.created' },
      });
      expect(events).toHaveLength(1);
      expect(events[0].status).toBe('PENDING');
      expect(events[0].aggregateId).toBe(first.id);

      const drained = await processor.drain();
      expect(drained.processed).toBe(1);
      const done = await prisma.domainEvent.findUniqueOrThrow({
        where: { id: events[0].id },
      });
      expect(done.status).toBe('DONE');
    });

    it('rejects bad payloads and hostile DTO fields', async () => {
      const a = await mkUser('valida@example.com');
      const b = await mkUser('validb@example.com');
      const conv = await direct(a.token, b.id);
      const bad = [
        { clientMessageId: randomUUID(), body: '' },
        { clientMessageId: randomUUID(), body: '   \n  ' },
        { clientMessageId: randomUUID(), body: 'x'.repeat(4001) },
        { body: 'missing key' },
        { clientMessageId: randomUUID() },
        // Privilege/injection fields are not whitelisted.
        { clientMessageId: randomUUID(), body: 'hi', senderId: b.id },
        { clientMessageId: randomUUID(), body: 'hi', conversationId: conv.id },
        { clientMessageId: randomUUID(), body: 'hi', role: 'OWNER' },
      ];
      for (const payload of bad) {
        const res = await api()
          .post(`/api/v1/conversations/${conv.id}/messages`)
          .set(auth(a.token))
          .send(payload)
          .expect(400);
        expect(res.body.error.code).toBe('VALIDATION_ERROR');
      }
      expect(await prisma.message.count()).toBe(0);
      // Direct creation also rejects injected fields.
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(a.token))
        .send({ userId: b.id, role: 'OWNER' })
        .expect(400);
    });

    it('stores markup verbatim as plain text (no interpretation)', async () => {
      const a = await mkUser('htmla@example.com');
      const b = await mkUser('htmlb@example.com');
      const conv = await direct(a.token, b.id);
      const evil = '<script>alert(1)</script><b>hi</b>';
      const sent = await send(a.token, conv.id, randomUUID(), evil);
      expect(sent.body).toBe(evil);
      const history = await api()
        .get(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(b.token))
        .expect(200);
      expect(history.body.data.items[0].body).toBe(evil);
    });

    it('rejects oversized request bodies with 413', async () => {
      const a = await mkUser('biga@example.com');
      const b = await mkUser('bigb@example.com');
      const conv = await direct(a.token, b.id);
      const res = await api()
        .post(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(a.token))
        .send({ clientMessageId: randomUUID(), body: 'x'.repeat(1_100_000) })
        .expect(413);
      expect(res.body.error.code).toBe('PAYLOAD_TOO_LARGE');
    });
  });

  // ------------------------------------------------------------- edit/delete
  describe('message editing + deletion', () => {
    it('sender edits; recipient cannot; deleted cannot be edited', async () => {
      const a = await mkUser('edita@example.com');
      const b = await mkUser('editb@example.com');
      const conv = await direct(a.token, b.id);
      const sent = await send(a.token, conv.id, randomUUID(), 'original here');

      const updated = await api()
        .patch(`/api/v1/messages/${sent.id}`)
        .set(auth(a.token))
        .send({ body: 'edited version' })
        .expect(200);
      expect(updated.body.data.body).toBe('edited version');
      expect(
        new Date(updated.body.data.updatedAt).getTime(),
      ).toBeGreaterThanOrEqual(new Date(sent.createdAt).getTime());
      expect(
        await prisma.domainEvent.count({ where: { type: 'message.updated' } }),
      ).toBe(1);

      // Recipient edit: 404 (no existence/ownership leak).
      await api()
        .patch(`/api/v1/messages/${sent.id}`)
        .set(auth(b.token))
        .send({ body: 'hijacked' })
        .expect(404);
      // Hostile edit DTO fields rejected.
      await api()
        .patch(`/api/v1/messages/${sent.id}`)
        .set(auth(a.token))
        .send({ body: 'x', senderId: b.id })
        .expect(400);
      await api()
        .patch(`/api/v1/messages/${sent.id}`)
        .set(auth(a.token))
        .send({ body: '   ' })
        .expect(400);
      await api()
        .patch('/api/v1/messages/not-a-uuid')
        .set(auth(a.token))
        .send({ body: 'x' })
        .expect(400);
      await api()
        .patch('/api/v1/messages/00000000-0000-4000-8000-000000000000')
        .set(auth(a.token))
        .send({ body: 'x' })
        .expect(404);

      await api().delete(`/api/v1/messages/${sent.id}`).set(auth(a.token)).expect(200);
      await api()
        .patch(`/api/v1/messages/${sent.id}`)
        .set(auth(a.token))
        .send({ body: 'resurrect?' })
        .expect(404);
    });

    it('sender soft-deletes once; repeat is idempotent; recipient cannot', async () => {
      const a = await mkUser('dela@example.com');
      const b = await mkUser('delb@example.com');
      const conv = await direct(a.token, b.id);
      const sent = await send(a.token, conv.id, randomUUID(), 'bye for now');

      await api().delete(`/api/v1/messages/${sent.id}`).set(auth(b.token)).expect(404);

      const removed = await api()
        .delete(`/api/v1/messages/${sent.id}`)
        .set(auth(a.token))
        .expect(200);
      expect(removed.body.data.deleted).toBe(true);

      const history = await api()
        .get(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(a.token))
        .expect(200);
      const tomb = history.body.data.items[0];
      expect(tomb.body).toBeNull();
      expect(tomb.isDeleted).toBe(true);
      expect(tomb.deletedAt).not.toBeNull();

      // Repeat deletion: idempotent, no second outbox event.
      await api().delete(`/api/v1/messages/${sent.id}`).set(auth(a.token)).expect(200);
      expect(
        await prisma.domainEvent.count({ where: { type: 'message.deleted' } }),
      ).toBe(1);
      // The row itself is never hard-deleted.
      expect(await prisma.message.count()).toBe(1);
    });
  });

  // --------------------------------------------------------------- pagination
  describe('cursor pagination', () => {
    it('walks message history without gaps/dups; rejects garbage cursors', async () => {
      const a = await mkUser('pagea@example.com');
      const b = await mkUser('pageb@example.com');
      const conv = await direct(a.token, b.id);
      const ids: string[] = [];
      for (let i = 0; i < 5; i += 1) {
        ids.push((await send(a.token, conv.id, randomUUID(), `msg ${i}`)).id);
        await tick();
      }
      const seen: string[] = [];
      let cursor: string | null = null;
      let pages = 0;
      for (;;) {
        const res: request.Response = await api()
          .get(
            `/api/v1/conversations/${conv.id}/messages?limit=2${
              cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''
            }`,
          )
          .set(auth(b.token))
          .expect(200);
        const { items, nextCursor }: { items: Array<{ id: string; createdAt: string }>; nextCursor: string | null } =
          res.body.data;
        expect(items.length).toBeGreaterThan(0);
        // Deterministic newest-first ordering over (createdAt, id).
        for (let i = 1; i < items.length; i += 1) {
          const prev = items[i - 1];
          const cur = items[i];
          const byTime =
            new Date(prev.createdAt).getTime() - new Date(cur.createdAt).getTime();
          expect(byTime > 0 || (byTime === 0 && prev.id > cur.id)).toBe(true);
        }
        seen.push(...items.map((m: { id: string }) => m.id));
        pages += 1;
        if (!nextCursor) break;
        cursor = nextCursor;
      }
      expect(pages).toBe(3);
      expect(new Set(seen).size).toBe(5);
      expect([...seen].sort()).toEqual([...ids].sort());

      await api()
        .get(`/api/v1/conversations/${conv.id}/messages?cursor=garbage!!`)
        .set(auth(b.token))
        .expect(400);
      await api()
        .get(`/api/v1/conversations/${conv.id}/messages?limit=0`)
        .set(auth(b.token))
        .expect(400);
      await api()
        .get(`/api/v1/conversations/${conv.id}/messages?limit=101`)
        .set(auth(b.token))
        .expect(400);
    });

    it('lists conversations newest-first with safe projections', async () => {
      const a = await mkUser('conva@example.com', 'Ann Lister');
      const b = await mkUser('convb@example.com', 'Bo Lister');
      const c = await mkUser('convc@example.com', 'Cy Lister');
      const d = await mkUser('convd@example.com', 'Dee Lister');
      const cb = await direct(a.token, b.id);
      await tick();
      const cc = await direct(a.token, c.id);
      await direct(a.token, d.id); // empty conversation sorts last
      await send(a.token, cb.id, randomUUID(), 'to bee');
      await tick();
      await send(c.token, cc.id, randomUUID(), 'to ann');

      const first = await api()
        .get('/api/v1/conversations?limit=1')
        .set(auth(a.token))
        .expect(200);
      expect(first.body.data.items).toHaveLength(1);
      expect(first.body.data.items[0].id).toBe(cc.id);
      expect(first.body.data.nextCursor).not.toBeNull();
      const peer = first.body.data.items[0].participant;
      expect(peer.displayName).toBe('Cy Lister');
      expect(peer.passwordHash).toBeUndefined();
      expect(peer.email).toBeUndefined();

      const second = await api()
        .get(
          `/api/v1/conversations?limit=1&cursor=${encodeURIComponent(
            first.body.data.nextCursor,
          )}`,
        )
        .set(auth(a.token))
        .expect(200);
      expect(second.body.data.items[0].id).toBe(cb.id);
      const third = await api()
        .get(
          `/api/v1/conversations?limit=1&cursor=${encodeURIComponent(
            second.body.data.nextCursor,
          )}`,
        )
        .set(auth(a.token))
        .expect(200);
      expect(third.body.data.items).toHaveLength(1);
      expect(third.body.data.items[0].lastMessage).toBeNull();
      expect(third.body.data.nextCursor).toBeNull();
    });
  });

  // --------------------------------------------------------------- read state
  describe('read state + unread counts', () => {
    it('tracks per-conversation and global unread; delete refunds unread', async () => {
      const a = await mkUser('reada6@example.com');
      const b = await mkUser('readb6@example.com');
      const conv = await direct(a.token, b.id);
      const m1 = await send(a.token, conv.id, randomUUID(), 'first unread');
      await send(a.token, conv.id, randomUUID(), 'second unread');

      const list = await api().get('/api/v1/conversations').set(auth(b.token)).expect(200);
      expect(list.body.data.items[0].unreadCount).toBe(2);
      const total = await api()
        .get('/api/v1/conversations/unread-count')
        .set(auth(b.token))
        .expect(200);
      expect(total.body.data.unread).toBe(2);

      // Deleting an unread message refunds the recipient counter exactly.
      await api().delete(`/api/v1/messages/${m1.id}`).set(auth(a.token)).expect(200);
      const afterDelete = await api()
        .get('/api/v1/conversations/unread-count')
        .set(auth(b.token))
        .expect(200);
      expect(afterDelete.body.data.unread).toBe(1);

      const read = await api()
        .post(`/api/v1/conversations/${conv.id}/read`)
        .set(auth(b.token))
        .expect(200);
      expect(read.body.data.unread).toBe(0);
      const zero = await api()
        .get('/api/v1/conversations/unread-count')
        .set(auth(b.token))
        .expect(200);
      expect(zero.body.data.unread).toBe(0);
      // Read is idempotent.
      await api().post(`/api/v1/conversations/${conv.id}/read`).set(auth(b.token)).expect(200);
    });

    it('never moves the read marker backwards', async () => {
      const a = await mkUser('monoa@example.com');
      const b = await mkUser('monob@example.com');
      const conv = await direct(a.token, b.id);
      await send(a.token, conv.id, randomUUID(), 'hello');
      const first = await api()
        .post(`/api/v1/conversations/${conv.id}/read`)
        .set(auth(b.token))
        .expect(200);
      // Simulate a racing older marker already persisted (clock skew/stale
      // write): the conditional update must refuse to regress past "now".
      const future = new Date(Date.now() + 60 * 60 * 1000);
      await prisma.conversationMember.update({
        where: {
          conversationId_userId: { conversationId: conv.id, userId: b.id },
        },
        data: { lastReadAt: future },
      });
      const second = await api()
        .post(`/api/v1/conversations/${conv.id}/read`)
        .set(auth(b.token))
        .expect(200);
      expect(new Date(second.body.data.lastReadAt).getTime()).toBe(future.getTime());
      expect(
        new Date(second.body.data.lastReadAt).getTime(),
      ).toBeGreaterThan(new Date(first.body.data.lastReadAt).getTime());
    });
  });

  // ------------------------------------------------------------------- blocks
  describe('block enforcement', () => {
    it('blocks stop new conversations and sends; history stays durable', async () => {
      const a = await mkUser('blocka@example.com');
      const b = await mkUser('blockb@example.com');
      const conv = await direct(a.token, b.id);
      const kept = await send(a.token, conv.id, randomUUID(), 'before the block');
      const eventsBefore = await prisma.domainEvent.count({
        where: { type: 'message.created' },
      });

      await api().post(`/api/v1/users/${b.id}/block`).set(auth(a.token)).expect(200);

      // Neither direction can open a new conversation or send.
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(b.token))
        .send({ userId: a.id })
        .expect(403);
      await api()
        .post('/api/v1/conversations/direct')
        .set(auth(a.token))
        .send({ userId: b.id })
        .expect(403);
      await api()
        .post(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(b.token))
        .send({ clientMessageId: randomUUID(), body: 'bypass?' })
        .expect(403);
      await api()
        .post(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(a.token))
        .send({ clientMessageId: randomUUID(), body: 'after block' })
        .expect(403);
      expect(await prisma.message.count()).toBe(1);
      // No new outbox event may be minted by a blocked send.
      expect(
        await prisma.domainEvent.count({ where: { type: 'message.created' } }),
      ).toBe(eventsBefore);

      // History remains durable for both members.
      for (const u of [a, b]) {
        const detail = await api()
          .get(`/api/v1/conversations/${conv.id}`)
          .set(auth(u.token))
          .expect(200);
        expect(detail.body.data.id).toBe(conv.id);
        const history = await api()
          .get(`/api/v1/conversations/${conv.id}/messages`)
          .set(auth(u.token))
          .expect(200);
        expect(history.body.data.items[0].id).toBe(kept.id);
      }

      // Unblock restores messaging on the same durable conversation.
      await api().delete(`/api/v1/users/${b.id}/block`).set(auth(a.token)).expect(200);
      await send(b.token, conv.id, randomUUID(), 'we are back');
      expect(await prisma.message.count()).toBe(2);
    });

    it('concurrent sends from a blocked user all fail with zero writes', async () => {
      const a = await mkUser('storma@example.com');
      const b = await mkUser('stormb@example.com');
      const conv = await direct(a.token, b.id);
      await api().post(`/api/v1/users/${b.id}/block`).set(auth(a.token)).expect(200);
      const attempts = await Promise.all(
        Array.from({ length: 10 }, (_, i) =>
          api()
            .post(`/api/v1/conversations/${conv.id}/messages`)
            .set(auth(b.token))
            .send({ clientMessageId: randomUUID(), body: `storm ${i}` }),
        ),
      );
      for (const r of attempts) expect(r.status).toBe(403);
      expect(await prisma.message.count()).toBe(0);
      expect(await prisma.domainEvent.count()).toBe(0);
    });
  });

  // --------------------------------------------------------------------- IDOR
  describe('authorization boundaries (IDOR matrix)', () => {
    it('non-members see 404 everywhere; malformed ids are 400', async () => {
      const a = await mkUser('idora@example.com');
      const b = await mkUser('idorb@example.com');
      const c = await mkUser('idorc@example.com');
      const conv = await direct(a.token, b.id);
      const msg = await send(a.token, conv.id, randomUUID(), 'private chat');

      await api().get(`/api/v1/conversations/${conv.id}`).set(auth(c.token)).expect(404);
      await api()
        .get(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(c.token))
        .expect(404);
      await api()
        .post(`/api/v1/conversations/${conv.id}/messages`)
        .set(auth(c.token))
        .send({ clientMessageId: randomUUID(), body: 'intrude' })
        .expect(404);
      await api()
        .post(`/api/v1/conversations/${conv.id}/read`)
        .set(auth(c.token))
        .expect(404);
      await api()
        .patch(`/api/v1/messages/${msg.id}`)
        .set(auth(c.token))
        .send({ body: 'intrude' })
        .expect(404);
      await api().delete(`/api/v1/messages/${msg.id}`).set(auth(c.token)).expect(404);

      const fake = '00000000-0000-4000-8000-000000000000';
      await api().get(`/api/v1/conversations/${fake}`).set(auth(a.token)).expect(404);
      await api().get('/api/v1/conversations/not-a-uuid').set(auth(a.token)).expect(400);
      await api()
        .get('/api/v1/conversations/not-a-uuid/messages')
        .set(auth(a.token))
        .expect(400);
      await api()
        .post('/api/v1/conversations/not-a-uuid/read')
        .set(auth(a.token))
        .expect(400);

      // Cross-user mutation attempts never transfer ownership.
      await api()
        .patch(`/api/v1/messages/${msg.id}`)
        .set(auth(b.token))
        .send({ body: 'mine now' })
        .expect(404);
      await api().delete(`/api/v1/messages/${msg.id}`).set(auth(b.token)).expect(404);
    });
  });

  // --------------------------------------------------------------- concurrency
  describe('concurrency invariants', () => {
    it('concurrent conversation creation yields exactly one conversation', async () => {
      const a = await mkUser('racesa@example.com');
      const b = await mkUser('racesb@example.com');
      const results = await Promise.all(
        Array.from({ length: 10 }, (_, i) =>
          api()
            .post('/api/v1/conversations/direct')
            .set(auth(i % 2 === 0 ? a.token : b.token))
            .send({ userId: i % 2 === 0 ? b.id : a.id }),
        ),
      );
      const ids = new Set<string>();
      for (const r of results) {
        expect([200, 201]).toContain(r.status);
        ids.add(r.body.data.id as string);
      }
      expect(ids.size).toBe(1);
      expect(await prisma.conversation.count()).toBe(1);
      expect(
        await prisma.conversationMember.count({
          where: { conversationId: [...ids][0] },
        }),
      ).toBe(2);
    });

    it('concurrent duplicate sends yield one message + one event', async () => {
      const a = await mkUser('dupsa@example.com');
      const b = await mkUser('dupsb@example.com');
      const conv = await direct(a.token, b.id);
      const key = randomUUID();
      const results = await Promise.all(
        Array.from({ length: 10 }, () =>
          api()
            .post(`/api/v1/conversations/${conv.id}/messages`)
            .set(auth(a.token))
            .send({ clientMessageId: key, body: 'race me' }),
        ),
      );
      const ids = new Set<string>();
      for (const r of results) {
        expect([200, 201]).toContain(r.status);
        ids.add(r.body.data.id as string);
      }
      expect(ids.size).toBe(1);
      expect(await prisma.message.count()).toBe(1);
      expect(
        await prisma.domainEvent.count({ where: { type: 'message.created' } }),
      ).toBe(1);
    });

    it('concurrent unique sends lose nothing and order deterministically', async () => {
      const a = await mkUser('parla@example.com');
      const b = await mkUser('parlb@example.com');
      const conv = await direct(a.token, b.id);
      const results = await Promise.all(
        Array.from({ length: 10 }, (_, i) =>
          api()
            .post(`/api/v1/conversations/${conv.id}/messages`)
            .set(auth(a.token))
            .send({ clientMessageId: randomUUID(), body: `parallel ${i}` }),
        ),
      );
      for (const r of results) expect(r.status).toBe(201);
      expect(await prisma.message.count()).toBe(10);
      const list = await api()
        .get(`/api/v1/conversations/${conv.id}/messages?limit=100`)
        .set(auth(b.token))
        .expect(200);
      const items = list.body.data.items as Array<{
        id: string;
        createdAt: string;
      }>;
      expect(items).toHaveLength(10);
      expect(new Set(items.map((m) => m.id)).size).toBe(10);
      for (let i = 1; i < items.length; i += 1) {
        const prev = items[i - 1];
        const cur = items[i];
        const byTime =
          new Date(prev.createdAt).getTime() - new Date(cur.createdAt).getTime();
        expect(byTime > 0 || (byTime === 0 && prev.id > cur.id)).toBe(true);
      }
    });
  });

  // ----------------------------------------------------------------- realtime
  describe('realtime delivery', () => {
    it('message.created reaches both members only, with a safe projection', async () => {
      const a = await mkUser('rta6@example.com', 'Rita Sender');
      const b = await mkUser('rtb6@example.com', 'Rex Receiver');
      const c = await mkUser('rtc6@example.com', 'Cas Stranger');
      const conv = await direct(a.token, b.id);

      const aSock = wsClient(a.token);
      const bSock = wsClient(b.token);
      const cSock = wsClient(c.token);
      await waitFor(aSock, 'connect');
      await waitFor(bSock, 'connect');
      await waitFor(cSock, 'connect');
      const aGot = waitFor<Record<string, unknown>>(aSock, 'message.created');
      const bGot = waitFor<Record<string, unknown>>(bSock, 'message.created');
      let cGot = false;
      cSock.once('message.created', () => {
        cGot = true;
      });

      const sent = await send(a.token, conv.id, randomUUID(), 'live hello');
      await processor.drain();

      const [pa, pb] = await Promise.all([aGot, bGot]);
      for (const p of [pa, pb]) {
        expect(p.id).toBe(sent.id);
        expect(p.conversationId).toBe(conv.id);
        expect(p.senderId).toBe(a.id);
        expect(p.body).toBe('live hello');
        expect(p.passwordHash).toBeUndefined();
      }
      await tick(500);
      expect(cGot).toBe(false);
      aSock.disconnect();
      bSock.disconnect();
      cSock.disconnect();
    });

    it('message.updated + message.deleted reach the recipient', async () => {
      const a = await mkUser('rua6@example.com');
      const b = await mkUser('rub6@example.com');
      const conv = await direct(a.token, b.id);
      const sent = await send(a.token, conv.id, randomUUID(), 'v1 text');
      await processor.drain();

      const bSock = wsClient(b.token);
      await waitFor(bSock, 'connect');

      const upd = waitFor<Record<string, unknown>>(bSock, 'message.updated');
      await api()
        .patch(`/api/v1/messages/${sent.id}`)
        .set(auth(a.token))
        .send({ body: 'v2 text' })
        .expect(200);
      await processor.drain();
      const gotUpd = await upd;
      expect(gotUpd.id).toBe(sent.id);
      expect(gotUpd.body).toBe('v2 text');

      const del = waitFor<Record<string, unknown>>(bSock, 'message.deleted');
      await api().delete(`/api/v1/messages/${sent.id}`).set(auth(a.token)).expect(200);
      await processor.drain();
      const gotDel = await del;
      expect(gotDel.id).toBe(sent.id);
      expect(gotDel.body).toBeNull();
      expect(gotDel.isDeleted).toBe(true);
      bSock.disconnect();
    });

    it('outbox redelivery never duplicates persisted messages', async () => {
      const a = await mkUser('redela@example.com');
      const b = await mkUser('redelb@example.com');
      const conv = await direct(a.token, b.id);
      const sent = await send(a.token, conv.id, randomUUID(), 'redeliver me');

      const bSock = wsClient(b.token);
      await waitFor(bSock, 'connect');
      const deliveries: string[] = [];
      bSock.on('message.created', (p: { id: string }) => {
        deliveries.push(p.id);
      });

      await processor.drain();
      const ev = await prisma.domainEvent.findFirstOrThrow({
        where: { type: 'message.created' },
      });
      await prisma.domainEvent.update({
        where: { id: ev.id },
        data: { status: 'PENDING', availableAt: new Date() },
      });
      await processor.drain();
      await tick(500);

      expect(await prisma.message.count()).toBe(1);
      expect(deliveries.length).toBeGreaterThanOrEqual(1);
      expect(new Set(deliveries)).toEqual(new Set([sent.id]));
      bSock.disconnect();
    });

    it('rejects anonymous, forged, wrong-secret, and expired sockets', async () => {
      async function expectTermination(sock: Socket): Promise<void> {
        const outcome = await new Promise<string>((resolve) => {
          const timer = setTimeout(() => resolve('no-decision'), 4000);
          sock.once('disconnect', () => {
            clearTimeout(timer);
            resolve('disconnected');
          });
        });
        expect(outcome).toBe('disconnected');
        sock.disconnect();
      }
      const a = await mkUser('socka@example.com');
      const secret = config.get<string>('jwt.accessSecret');
      const forged = jwt.sign(
        { sub: a.id, role: 'STUDENT', jti: randomUUID() },
        { secret: `${secret}-wrong`, expiresIn: 900 },
      );
      const expired = jwt.sign(
        {
          sub: a.id,
          role: 'STUDENT',
          jti: randomUUID(),
          exp: Math.floor(Date.now() / 1000) - 60,
        },
        { secret },
      );
      await expectTermination(wsClient());
      await expectTermination(wsClient('not-a-jwt'));
      await expectTermination(wsClient(forged));
      await expectTermination(wsClient(expired));
    });
  });

  // ------------------------------------------------------------- rate limiting
  describe('message rate limiting (30/min/user)', () => {
    it('limits the sender after 30 sends; other users unaffected', async () => {
      const a = await mkUser('limita@example.com');
      const b = await mkUser('limitb@example.com');
      const convA = await direct(a.token, b.id);
      for (let i = 0; i < 30; i += 1) {
        await send(a.token, convA.id, randomUUID(), `quota ${i}`);
      }
      const denied = await api()
        .post(`/api/v1/conversations/${convA.id}/messages`)
        .set(auth(a.token))
        .send({ clientMessageId: randomUUID(), body: 'over quota' })
        .expect(429);
      expect(denied.body.error.code).toBe('RATE_LIMITED');
      expect(denied.headers['x-ratelimit-limit']).toBe('30');

      // Same IP, different user: per-user scoping leaves B unaffected.
      const convB = await direct(b.token, a.id, 200);
      await send(b.token, convB.id, randomUUID(), 'b still can');
      expect(await prisma.message.count()).toBe(31);
    });
  });
});

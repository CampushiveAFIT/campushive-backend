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
const DAY = 24 * 60 * 60 * 1000;

// Deterministic tests drive the processor explicitly; the background
// interval is parked so assertions never race it.
process.env.OUTBOX_POLL_MS = '3600000';

describe('Slice 5: notifications + realtime', () => {
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

  async function mkUniversity(slug: string) {
    return prisma.university.create({ data: { name: slug, slug } });
  }

  async function join(token: string, universityId: string) {
    await api()
      .post('/api/v1/users/me/university')
      .set(auth(token))
      .send({ universityId })
      .expect(200);
  }

  async function mkPost(token: string, body: Record<string, unknown>) {
    const res = await api()
      .post('/api/v1/posts')
      .set(auth(token))
      .send(body)
      .expect(201);
    return res.body.data.id as string;
  }

  async function mkEvent(token: string, over: Record<string, unknown> = {}) {
    const start = new Date(Date.now() + 7 * DAY).toISOString();
    const created = await api()
      .post('/api/v1/events')
      .set(auth(token))
      .send({ title: 'Test Event', startsAt: start, capacity: 50, ...over })
      .expect(201);
    const id = created.body.data.id as string;
    await api()
      .patch(`/api/v1/events/${id}`)
      .set(auth(token))
      .send({ status: 'PUBLISHED' })
      .expect(200);
    return id;
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

  // ------------------------------------------------------------ outbox core
  describe('transactional outbox', () => {
    it('persists the event atomically with the like, before processing', async () => {
      const uni = await mkUniversity('obx-uni');
      const a = await mkUser('obxa@example.com');
      const b = await mkUser('obxb@example.com');
      await join(a.token, uni.id);
      await join(b.token, uni.id);
      const postId = await mkPost(a.token, { content: 'outbox me' });
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(b.token)).expect(200);

      const pending = await prisma.domainEvent.findMany();
      expect(pending).toHaveLength(1);
      expect(pending[0].type).toBe('post.liked');
      expect(pending[0].status).toBe('PENDING');
      expect(
        await prisma.notification.count({ where: { recipientId: a.id } }),
      ).toBe(0);

      const drained = await processor.drain();
      expect(drained.processed).toBe(1);
      const done = await prisma.domainEvent.findUniqueOrThrow({
        where: { id: pending[0].id },
      });
      expect(done.status).toBe('DONE');
      expect(done.processedAt).not.toBeNull();
    });

    it('redelivery is idempotent (dedupeKey)', async () => {
      const uni = await mkUniversity('obx2-uni');
      const a = await mkUser('obx2a@example.com');
      const b = await mkUser('obx2b@example.com');
      await join(a.token, uni.id);
      await join(b.token, uni.id);
      const postId = await mkPost(a.token, { content: 'dedupe me' });
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(b.token)).expect(200);
      await processor.drain();
      await processor.drain();
      // Force a second processing pass by resetting the event row.
      const ev = await prisma.domainEvent.findFirstOrThrow();
      await prisma.domainEvent.update({
        where: { id: ev.id },
        data: { status: 'PENDING', availableAt: new Date() },
      });
      await processor.drain();
      expect(
        await prisma.notification.count({ where: { recipientId: a.id } }),
      ).toBe(1);
    });
  });

  // -------------------------------------------------------------- delivery
  describe('notification delivery', () => {
    it('like → recipient notified with actor + post reference', async () => {
      const uni = await mkUniversity('dlv-uni');
      const author = await mkUser('dlva@example.com', 'Ada Author');
      const fan = await mkUser('dlvf@example.com', 'Finn Fan');
      await join(author.token, uni.id);
      await join(fan.token, uni.id);
      const postId = await mkPost(author.token, { content: 'hello world' });
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(fan.token)).expect(200);
      await processor.drain();

      const list = await api()
        .get('/api/v1/notifications')
        .set(auth(author.token))
        .expect(200);
      expect(list.body.data.items).toHaveLength(1);
      const n = list.body.data.items[0];
      expect(n.type).toBe('LIKE');
      expect(n.title).toContain('Finn Fan');
      expect(n.data.postId).toBe(postId);
      expect(n.actor.id).toBe(fan.id);
      expect(n.isRead).toBe(false);
      expect(n.passwordHash).toBeUndefined();
      const count = await api()
        .get('/api/v1/notifications/unread-count')
        .set(auth(author.token))
        .expect(200);
      expect(count.body.data.unread).toBe(1);
    });

    it('self-like and self-comment produce nothing', async () => {
      const uni = await mkUniversity('self-uni');
      const me = await mkUser('selfme@example.com');
      await join(me.token, uni.id);
      const postId = await mkPost(me.token, { content: 'my own post' });
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(me.token)).expect(200);
      await api()
        .post(`/api/v1/posts/${postId}/comments`)
        .set(auth(me.token))
        .send({ content: 'my own comment' })
        .expect(201);
      await processor.drain();
      expect(
        await prisma.notification.count({ where: { recipientId: me.id } }),
      ).toBe(0);
      expect(await prisma.domainEvent.count()).toBe(2);
    });

    it('comment → recipient notified with excerpt', async () => {
      const uni = await mkUniversity('cmt-n-uni');
      const author = await mkUser('cmta@example.com', 'Cora Author');
      const commenter = await mkUser('cmtc@example.com', 'Cole Commenter');
      await join(author.token, uni.id);
      await join(commenter.token, uni.id);
      const postId = await mkPost(author.token, { content: 'discuss this' });
      await api()
        .post(`/api/v1/posts/${postId}/comments`)
        .set(auth(commenter.token))
        .send({ content: 'great point, fully agree here' })
        .expect(201);
      await processor.drain();
      const list = await api()
        .get('/api/v1/notifications')
        .set(auth(author.token))
        .expect(200);
      expect(list.body.data.items).toHaveLength(1);
      expect(list.body.data.items[0].type).toBe('COMMENT');
      expect(list.body.data.items[0].body).toContain('great point');
    });

    it('follow → followed user notified', async () => {
      const a = await mkUser('fola@example.com', 'Fay Follower');
      const b = await mkUser('folb@example.com');
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
      await processor.drain();
      const list = await api()
        .get('/api/v1/notifications')
        .set(auth(b.token))
        .expect(200);
      expect(list.body.data.items).toHaveLength(1);
      expect(list.body.data.items[0].type).toBe('FOLLOW');
      expect(list.body.data.items[0].actor.id).toBe(a.id);
    });

    it('event cancellation notifies ACTIVE holders only', async () => {
      const uni = await mkUniversity('cx-uni');
      const org = await mkUser('cxorg@example.com');
      const h1 = await mkUser('cxh1@example.com');
      const h2 = await mkUser('cxh2@example.com');
      const stranger = await mkUser('cxx@example.com');
      for (const u of [org, h1, h2, stranger]) await join(u.token, uni.id);
      const id = await mkEvent(org.token, { title: 'Doomed Fest' });
      await api().post(`/api/v1/events/${id}/register`).set(auth(h1.token)).expect(200);
      await api().post(`/api/v1/events/${id}/register`).set(auth(h2.token)).expect(200);
      await api().delete(`/api/v1/events/${id}`).set(auth(org.token)).expect(200);
      await processor.drain();

      for (const h of [h1, h2]) {
        const list = await api()
          .get('/api/v1/notifications')
          .set(auth(h.token))
          .expect(200);
        expect(list.body.data.items).toHaveLength(1);
        expect(list.body.data.items[0].type).toBe('EVENT_CANCELLED');
        expect(list.body.data.items[0].data.eventId).toBe(id);
      }
      const none = await api()
        .get('/api/v1/notifications')
        .set(auth(stranger.token))
        .expect(200);
      expect(none.body.data.items).toHaveLength(0);
      const orgList = await api()
        .get('/api/v1/notifications')
        .set(auth(org.token))
        .expect(200);
      expect(orgList.body.data.items).toHaveLength(0);
    });
  });

  // ------------------------------------------------------------ preferences
  describe('preferences', () => {
    it('defaults all true; disabled types suppress creation', async () => {
      const uni = await mkUniversity('pref-uni');
      const author = await mkUser('prefa@example.com');
      const fan = await mkUser('preff@example.com');
      await join(author.token, uni.id);
      await join(fan.token, uni.id);
      const defaults = await api()
        .get('/api/v1/users/me/notification-preferences')
        .set(auth(author.token))
        .expect(200);
      expect(defaults.body.data).toMatchObject({
        likesEnabled: true,
        commentsEnabled: true,
        followsEnabled: true,
        eventUpdatesEnabled: true,
      });
      await api()
        .patch('/api/v1/users/me/notification-preferences')
        .set(auth(author.token))
        .send({ likesEnabled: false })
        .expect(200);
      const postId = await mkPost(author.token, { content: 'muted likes' });
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(fan.token)).expect(200);
      await processor.drain();
      expect(
        await prisma.notification.count({ where: { recipientId: author.id } }),
      ).toBe(0);
      // Re-enable restores delivery (unlike first so a new like is created).
      await api()
        .patch('/api/v1/users/me/notification-preferences')
        .set(auth(author.token))
        .send({ likesEnabled: true })
        .expect(200);
      await api()
        .delete(`/api/v1/posts/${postId}/like`)
        .set(auth(fan.token))
        .expect(200);
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(fan.token)).expect(200);
      await processor.drain();
      expect(
        await prisma.notification.count({ where: { recipientId: author.id } }),
      ).toBe(1);
    });

    it('rejects unknown fields and non-booleans', async () => {
      const me = await mkUser('prefbad@example.com');
      await api()
        .patch('/api/v1/users/me/notification-preferences')
        .set(auth(me.token))
        .send({ likesEnabled: 'yes' })
        .expect(400);
      await api()
        .patch('/api/v1/users/me/notification-preferences')
        .set(auth(me.token))
        .send({ role: 'ADMIN' })
        .expect(400);
      await api()
        .get('/api/v1/users/me/notification-preferences')
        .expect(401);
    });
  });

  // ------------------------------------------------------------ read state
  describe('read state + IDOR', () => {
    it('unread filter, mark read idempotent, read-all, foreign 404', async () => {
      const uni = await mkUniversity('read-uni');
      const author = await mkUser('reada@example.com');
      const fan = await mkUser('readf@example.com');
      const stranger = await mkUser('reads@example.com');
      await join(author.token, uni.id);
      await join(fan.token, uni.id);
      const p1 = await mkPost(author.token, { content: 'post one here' });
      const p2 = await mkPost(author.token, { content: 'post two here' });
      await api().post(`/api/v1/posts/${p1}/like`).set(auth(fan.token)).expect(200);
      await api().post(`/api/v1/posts/${p2}/like`).set(auth(fan.token)).expect(200);
      await processor.drain();

      const all = await api()
        .get('/api/v1/notifications')
        .set(auth(author.token))
        .expect(200);
      expect(all.body.data.items).toHaveLength(2);
      const [first] = all.body.data.items;

      // Foreign user cannot read it (404, no leak).
      await api()
        .post(`/api/v1/notifications/${first.id}/read`)
        .set(auth(stranger.token))
        .expect(404);
      await api()
        .post('/api/v1/notifications/00000000-0000-4000-8000-000000000000/read')
        .set(auth(author.token))
        .expect(404);

      const read = await api()
        .post(`/api/v1/notifications/${first.id}/read`)
        .set(auth(author.token))
        .expect(200);
      expect(read.body.data.isRead).toBe(true);
      // Idempotent second read.
      await api()
        .post(`/api/v1/notifications/${first.id}/read`)
        .set(auth(author.token))
        .expect(200);

      const unreadOnly = await api()
        .get('/api/v1/notifications?unread=true')
        .set(auth(author.token))
        .expect(200);
      expect(unreadOnly.body.data.items).toHaveLength(1);

      const marked = await api()
        .post('/api/v1/notifications/read-all')
        .set(auth(author.token))
        .expect(200);
      expect(marked.body.data.marked).toBe(1);
      const count = await api()
        .get('/api/v1/notifications/unread-count')
        .set(auth(author.token))
        .expect(200);
      expect(count.body.data.unread).toBe(0);
    });
  });

  // --------------------------------------------------------------- realtime
  describe('realtime delivery', () => {
    it('delivers notification.created to the recipient room only', async () => {
      const uni = await mkUniversity('rt-uni');
      const author = await mkUser('rta@example.com');
      const fan = await mkUser('rtf@example.com');
      const other = await mkUser('rto@example.com');
      await join(author.token, uni.id);
      await join(fan.token, uni.id);
      const postId = await mkPost(author.token, { content: 'live post here' });

      const recipientSock = wsClient(author.token);
      const otherSock = wsClient(other.token);
      await waitFor(recipientSock, 'connect');
      await waitFor(otherSock, 'connect');
      const received = waitFor<{ id: string; type: string }>(
        recipientSock,
        'notification.created',
      );
      let otherGot = false;
      otherSock.once('notification.created', () => {
        otherGot = true;
      });

      await api().post(`/api/v1/posts/${postId}/like`).set(auth(fan.token)).expect(200);
      await processor.drain();

      const payload = await received;
      expect(payload.type).toBe('LIKE');
      expect(typeof payload.id).toBe('string');
      // Give the other socket a beat to (not) receive anything.
      await new Promise((r) => setTimeout(r, 500));
      expect(otherGot).toBe(false);
      recipientSock.disconnect();
      otherSock.disconnect();
    });

    it('rejects unauthenticated sockets and bad tokens', async () => {
      // The server terminates unauthorized sockets: a disconnect must follow.
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
      await expectTermination(wsClient());
      await expectTermination(wsClient('not-a-jwt'));
    });
  });

  // ------------------------------------------------- blocked interactions
  describe('blocks suppress notifications', () => {
    it('blocked authors like attempt fails at the gate; no outbox row', async () => {
      const uni = await mkUniversity('blk-n-uni');
      const a = await mkUser('blka@example.com');
      const b = await mkUser('blkb@example.com');
      await join(a.token, uni.id);
      await join(b.token, uni.id);
      const postId = await mkPost(a.token, { content: 'blocked target post' });
      await api().post(`/api/v1/users/${a.id}/block`).set(auth(b.token)).expect(200);
      await api().post(`/api/v1/posts/${postId}/like`).set(auth(b.token)).expect(404);
      expect(await prisma.domainEvent.count()).toBe(0);
      await processor.drain();
      expect(await prisma.notification.count()).toBe(0);
    });
  });
});

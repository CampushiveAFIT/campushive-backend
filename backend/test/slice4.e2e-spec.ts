import { INestApplication } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import { Test, TestingModule } from '@nestjs/testing';
import { argon2id, hash } from 'argon2';
import { randomUUID } from 'crypto';
import request from 'supertest';
import { AppModule } from '../src/app.module';
import { PrismaService } from '../src/config/prisma.service';
import { EmailService } from '../src/common/email/email.service';
import { REDIS_CLIENT } from '../src/common/redis/redis.module';
import type Redis from 'ioredis';

const PASSWORD = 'Str0ng-Passw0rd!';
const DAY = 24 * 60 * 60 * 1000;

interface FeedItemShape {
  type: 'POST' | 'EVENT';
  id: string;
  createdAt: string;
  score: number;
  post?: Record<string, unknown>;
  event?: Record<string, unknown>;
}

describe('Slice 4: feeds + search', () => {
  let app: INestApplication;
  let prisma: PrismaService;
  let redis: Redis;
  let jwt: JwtService;
  let config: ConfigService;
  let passwordHash: string;

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

    prisma = app.get(PrismaService);
    redis = app.get(REDIS_CLIENT);
    jwt = app.get(JwtService);
    config = app.get(ConfigService);
    passwordHash = await hash(PASSWORD, { type: argon2id });
  });

  afterAll(async () => {
    await app.close();
  });

  beforeEach(async () => {
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

  async function mkPost(
    token: string,
    body: Record<string, unknown>,
    expected = 201,
  ) {
    return api().post('/api/v1/posts').set(auth(token)).send(body).expect(expected);
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

  async function mkCommunity(
    token: string,
    universityId: string,
    slug: string,
    visibility = 'UNIVERSITY',
  ) {
    const res = await api()
      .post('/api/v1/communities')
      .set(auth(token))
      .send({ universityId, name: slug, slug, visibility })
      .expect(201);
    return res.body.data.id as string;
  }

  function ids(items: FeedItemShape[]): string[] {
    return items.map((i) => i.id);
  }

  // ------------------------------------------------------------------ home
  describe('GET /feed/home', () => {
    it('mixes followed, university, community, and event content', async () => {
      const uni = await mkUniversity('home-uni');
      const me = await mkUser('home-me@example.com');
      const friend = await mkUser('home-friend@example.com');
      await join(me.token, uni.id);
      await join(friend.token, uni.id);
      await api().post(`/api/v1/users/${friend.id}/follow`).set(auth(me.token)).expect(200);

      const fp = (
        await mkPost(friend.token, { content: 'friend post', visibility: 'FOLLOWERS' })
      ).body.data.id as string;
      const up = (
        await mkPost(friend.token, { content: 'uni post' })
      ).body.data.id as string;
      const ev = await mkEvent(friend.token, { title: 'Uni Gig' });

      const res = await api().get('/api/v1/feed/home').set(auth(me.token)).expect(200);
      const items = res.body.data.items as FeedItemShape[];
      expect(ids(items)).toEqual(expect.arrayContaining([fp, up, ev]));
      for (const item of items) {
        expect(['POST', 'EVENT']).toContain(item.type);
        expect(typeof item.score).toBe('number');
        expect(typeof item.createdAt).toBe('string');
        if (item.type === 'POST') {
          expect(item.post).toBeDefined();
          expect(item.event).toBeUndefined();
        } else {
          expect(item.event).toBeDefined();
          expect(item.post).toBeUndefined();
          expect(item.event).not.toHaveProperty('tickets');
        }
      }
    });

    it('excludes foreign, blocked, and invisible content', async () => {
      const a = await mkUniversity('home-a');
      const b = await mkUniversity('home-b');
      const me = await mkUser('home-me2@example.com');
      const stranger = await mkUser('home-stranger@example.com');
      await join(me.token, a.id);
      await join(stranger.token, b.id);

      const foreignUni = (
        await mkPost(stranger.token, { content: 'foreign uni post' })
      ).body.data.id as string;
      const foreignPub = (
        await mkPost(stranger.token, { content: 'foreign public post', visibility: 'PUBLIC' })
      ).body.data.id as string;
      const mine = (
        await mkPost(me.token, { content: 'my post' })
      ).body.data.id as string;
      // Block the stranger: their content must vanish even if public-eligible.
      await api().post(`/api/v1/users/${stranger.id}/block`).set(auth(me.token)).expect(200);

      const res = await api().get('/api/v1/feed/home').set(auth(me.token)).expect(200);
      const got = ids(res.body.data.items as FeedItemShape[]);
      expect(got).not.toContain(foreignUni);
      expect(got).not.toContain(foreignPub);
      expect(got).toContain(mine);
    });

    it('requires authentication', async () => {
      await api().get('/api/v1/feed/home').expect(401);
    });
  });

  // ------------------------------------------------------------- university
  describe('GET /feed/university', () => {
    it('defaults to primary; rejects foreign universities', async () => {
      const a = await mkUniversity('ufe-a');
      const b = await mkUniversity('ufe-b');
      const me = await mkUser('ufe-me@example.com');
      const other = await mkUser('ufe-other@example.com');
      await join(me.token, a.id);
      await join(other.token, b.id);
      const p = (
        await mkPost(me.token, { content: 'a post' })
      ).body.data.id as string;

      const def = await api().get('/api/v1/feed/university').set(auth(me.token)).expect(200);
      expect(ids(def.body.data.items as FeedItemShape[])).toContain(p);
      await api()
        .get(`/api/v1/feed/university?universityId=${b.id}`)
        .set(auth(me.token))
        .expect(403);
      // Non-member cannot read the feed at all.
      await api()
        .get(`/api/v1/feed/university?universityId=${a.id}`)
        .set(auth(other.token))
        .expect(403);
    });
  });

  // ------------------------------------------------------------- community
  describe('GET /feed/community/:id', () => {
    it('members see posts+events; private feed gated; missing 404', async () => {
      const uni = await mkUniversity('cfe-uni');
      const owner = await mkUser('cfe-owner@example.com');
      const outsider = await mkUser('cfe-out@example.com');
      await join(owner.token, uni.id);
      await join(outsider.token, uni.id);
      const cid = await mkCommunity(owner.token, uni.id, 'cfe-club', 'PRIVATE');
      const post = await mkPost(owner.token, {
        content: 'club news',
        visibility: 'COMMUNITY',
        communityId: cid,
      });
      const pid = post.body.data.id as string;
      const ev = await mkEvent(owner.token, {
        title: 'Club Night',
        visibility: 'COMMUNITY',
        communityId: cid,
      });

      await api().get(`/api/v1/feed/community/${cid}`).set(auth(outsider.token)).expect(403);
      const res = await api()
        .get(`/api/v1/feed/community/${cid}`)
        .set(auth(owner.token))
        .expect(200);
      expect(ids(res.body.data.items as FeedItemShape[])).toEqual(
        expect.arrayContaining([pid, ev]),
      );
      await api()
        .get('/api/v1/feed/community/00000000-0000-4000-8000-000000000000')
        .set(auth(owner.token))
        .expect(404);
    });
  });

  // --------------------------------------------------------------- explore
  describe('GET /feed/explore', () => {
    it('shows only PUBLIC posts and events across universities', async () => {
      const a = await mkUniversity('exp-a');
      const b = await mkUniversity('exp-b');
      const ua = await mkUser('exp-ua@example.com');
      const ub = await mkUser('exp-ub@example.com');
      await join(ua.token, a.id);
      await join(ub.token, b.id);
      const pubPost = (
        await mkPost(ua.token, { content: 'hello explore', visibility: 'PUBLIC' })
      ).body.data.id as string;
      const privPost = (
        await mkPost(ua.token, { content: 'not for explore' })
      ).body.data.id as string;
      const pubEvent = await mkEvent(ua.token, { title: 'Open Expo', visibility: 'PUBLIC' });
      await mkEvent(ua.token, { title: 'Hidden Expo' });

      const res = await api().get('/api/v1/feed/explore').set(auth(ub.token)).expect(200);
      const got = ids(res.body.data.items as FeedItemShape[]);
      expect(got).toContain(pubPost);
      expect(got).toContain(pubEvent);
      expect(got).not.toContain(privPost);
    });

    it('requires authentication', async () => {
      await api().get('/api/v1/feed/explore').expect(401);
    });
  });

  // ------------------------------------------------------------- pagination
  describe('feed pagination', () => {
    it('pages without overlap and rejects bad cursors', async () => {
      const uni = await mkUniversity('page-uni');
      const me = await mkUser('page-me@example.com');
      await join(me.token, uni.id);
      for (let i = 0; i < 3; i++) {
        await mkPost(me.token, { content: `post number ${i}`, visibility: 'PUBLIC' });
      }
      const p1 = await api()
        .get('/api/v1/feed/home?limit=2')
        .set(auth(me.token))
        .expect(200);
      expect(p1.body.data.items).toHaveLength(2);
      expect(typeof p1.body.data.nextCursor).toBe('string');
      const p2 = await api()
        .get(`/api/v1/feed/home?limit=2&cursor=${encodeURIComponent(p1.body.data.nextCursor as string)}`)
        .set(auth(me.token))
        .expect(200);
      expect(p2.body.data.items).toHaveLength(1);
      expect(p2.body.data.nextCursor).toBeNull();
      const overlap = ids(p1.body.data.items as FeedItemShape[]).filter((id) =>
        ids(p2.body.data.items as FeedItemShape[]).includes(id),
      );
      expect(overlap).toHaveLength(0);
      await api()
        .get('/api/v1/feed/home?cursor=garbage!!!')
        .set(auth(me.token))
        .expect(400);
      await api().get('/api/v1/feed/home?limit=99').set(auth(me.token)).expect(400);
    });
  });

  // ---------------------------------------------------------------- ranking
  describe('ranking', () => {
    it('engagement boosts an older post above a newer quiet one', async () => {
      const uni = await mkUniversity('rank-uni');
      const me = await mkUser('rank-me@example.com');
      const fan = await mkUser('rank-fan@example.com');
      await join(me.token, uni.id);
      await join(fan.token, uni.id);
      const old = (
        await mkPost(me.token, { content: 'older but loved', visibility: 'PUBLIC' })
      ).body.data.id as string;
      await api().post(`/api/v1/posts/${old}/like`).set(auth(fan.token)).expect(200);
      await api().post(`/api/v1/posts/${old}/like`).set(auth(me.token)).expect(200);
      const fresh = (
        await mkPost(me.token, { content: 'brand new silence', visibility: 'PUBLIC' })
      ).body.data.id as string;

      const res = await api().get('/api/v1/feed/explore').set(auth(fan.token)).expect(200);
      const items = res.body.data.items as FeedItemShape[];
      const order = ids(items);
      // 2 likes ≈ +4h boost > seconds of age gap.
      expect(order.indexOf(old)).toBeLessThan(order.indexOf(fresh));
      expect(items[0].score).toBeGreaterThan(items[1].score);
    });
  });

  // ----------------------------------------------------------------- search
  describe('GET /search', () => {
    it('finds across all five domains with visibility respected', async () => {
      const uni = await mkUniversity('search-uni');
      const me = await mkUser('search-me@example.com', 'Ada Searcher');
      const pal = await mkUser('search-pal@example.com', 'Ada Lover');
      await join(me.token, uni.id);
      await join(pal.token, uni.id);
      await mkCommunity(me.token, uni.id, 'ada-club');
      await mkEvent(me.token, { title: 'Ada Lovelace Night', visibility: 'PUBLIC' });
      await mkPost(me.token, { content: 'all about ada', visibility: 'PUBLIC' });
      await mkPost(me.token, { content: 'ada secrets here' });

      const res = await api()
        .get('/api/v1/search?q=ada')
        .set(auth(pal.token))
        .expect(200);
      const data = res.body.data as Record<string, Array<{ id: string }>>;
      expect(data.users.map((u) => u.id)).toEqual(
        expect.arrayContaining([me.id, pal.id]),
      );
      expect(data.communities).toHaveLength(1);
      expect(data.events).toHaveLength(1);
      expect(data.posts).toHaveLength(2);
      // No sensitive fields leak.
      expect(JSON.stringify(res.body)).not.toContain('passwordHash');
      expect(JSON.stringify(res.body)).not.toContain('@example.com');

      // Type filter narrows to one domain.
      const only = await api()
        .get('/api/v1/search?q=ada&type=events')
        .set(auth(pal.token))
        .expect(200);
      expect(only.body.data.events).toHaveLength(1);
      expect(only.body.data.posts).toHaveLength(0);
      expect(only.body.data.users).toHaveLength(0);
    });

    it('hides private communities and invisible content from outsiders', async () => {
      const uni = await mkUniversity('spriv-uni');
      const owner = await mkUser('spriv-o@example.com');
      const outsider = await mkUser('spriv-x@example.com');
      await join(owner.token, uni.id);
      await join(outsider.token, uni.id);
      await mkCommunity(owner.token, uni.id, 'zebrastripe-club', 'PRIVATE');
      await mkEvent(owner.token, { title: 'Zebrastripe Summit', visibility: 'PUBLIC' });

      const res = await api()
        .get('/api/v1/search?q=zebrastripe')
        .set(auth(outsider.token))
        .expect(200);
      expect(res.body.data.communities).toHaveLength(0);
      expect(res.body.data.events).toHaveLength(1);
    });

    it('validates query, type, auth, and limits', async () => {
      const me = await mkUser('sval@example.com');
      await api().get('/api/v1/search?q=a').set(auth(me.token)).expect(400);
      await api().get('/api/v1/search?q=abc&type=bogus').set(auth(me.token)).expect(400);
      await api().get('/api/v1/search?q=abc').expect(401);
      await api().get('/api/v1/search?q=abc&limit=99').set(auth(me.token)).expect(400);
    });
  });
});

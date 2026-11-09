import { INestApplication } from '@nestjs/common';
import { Test, TestingModule } from '@nestjs/testing';
import request from 'supertest';
import { argon2id, hash } from 'argon2';
import { AppModule } from '../src/app.module';
import { PrismaService } from '../src/config/prisma.service';
import { EmailService } from '../src/common/email/email.service';
import { REDIS_CLIENT } from '../src/common/redis/redis.module';
import type Redis from 'ioredis';

const PASSWORD = 'Str0ng-Passw0rd!';

describe('Slice 2: campus identity + social domain', () => {
  let app: INestApplication;
  let prisma: PrismaService;
  let redis: Redis;
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
    passwordHash = await hash(PASSWORD, { type: argon2id });
  });

  afterAll(async () => {
    await app.close();
  });

  beforeEach(async () => {
    await prisma.like.deleteMany();
    await prisma.comment.deleteMany();
    await prisma.post.deleteMany();
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

  async function register(email: string, expected = 201) {
    return api()
      .post('/api/v1/auth/register')
      .send({ email, password: PASSWORD, displayName: email.split('@')[0] })
      .expect(expected);
  }

  function login(email: string) {
    return api().post('/api/v1/auth/login').send({ email, password: PASSWORD });
  }

  async function mkUser(email: string) {
    await register(email);
    const res = await login(email).expect(200);
    return {
      id: res.body.data.user.id as string,
      token: res.body.data.accessToken as string,
    };
  }

  /** Bulk fixture users bypassing rate limits (same Argon2id hash). */
  async function mkUsers(n: number, prefix: string) {
    const users: { id: string; email: string }[] = [];
    for (let i = 0; i < n; i++) {
      const email = `${prefix}${i}@example.com`;
      const u = await prisma.user.create({
        data: { email, passwordHash, displayName: `${prefix}${i}` },
      });
      users.push({ id: u.id, email });
    }
    // Log each in to obtain tokens (login limit is generous here).
    const out: { id: string; email: string; token: string }[] = [];
    for (const u of users) {
      const res = await login(u.email).expect(200);
      out.push({ ...u, token: res.body.data.accessToken as string });
    }
    return out;
  }

  async function mkUniversity(name = 'Test University', slug = 'test-uni') {
    return prisma.university.create({ data: { name, slug } });
  }

  async function joinUniversity(token: string, universityId: string, expected = 200) {
    return api()
      .post('/api/v1/users/me/university')
      .set('Authorization', `Bearer ${token}`)
      .send({ universityId })
      .expect(expected);
  }

  async function mkCommunity(
    token: string,
    body: Record<string, unknown>,
    expected = 201,
  ) {
    return api()
      .post('/api/v1/communities')
      .set('Authorization', `Bearer ${token}`)
      .send(body)
      .expect(expected);
  }

  const auth = (token: string) => ({ Authorization: `Bearer ${token}` });

  // ---------------------------------------------------------- universities
  describe('universities', () => {
    it('lists active universities and hides inactive ones', async () => {
      await mkUniversity('Alpha University', 'alpha-uni');
      await mkUniversity('Beta University', 'beta-uni');
      const inactive = await mkUniversity('Dead University', 'dead-uni');
      await prisma.university.update({
        where: { id: inactive.id },
        data: { isActive: false },
      });
      const me = await mkUser('listuni@example.com');
      const res = await api()
        .get('/api/v1/universities')
        .set(auth(me.token))
        .expect(200);
      const slugs = res.body.data.items.map((u: { slug: string }) => u.slug);
      expect(slugs).toContain('alpha-uni');
      expect(slugs).toContain('beta-uni');
      expect(slugs).not.toContain('dead-uni');
    });

    it('returns detail with counts; 404 for unknown/inactive slugs', async () => {
      const uni = await mkUniversity('Count University', 'count-uni');
      const me = await mkUser('countme@example.com');
      await joinUniversity(me.token, uni.id);
      const res = await api()
        .get('/api/v1/universities/count-uni')
        .set(auth(me.token))
        .expect(200);
      expect(res.body.data.memberCount).toBe(1);
      await api()
        .get('/api/v1/universities/nope')
        .set(auth(me.token))
        .expect(404);
    });

    it('requires authentication', async () => {
      await api().get('/api/v1/universities').expect(401);
    });
  });

  // ------------------------------------------------- university membership
  describe('university membership', () => {
    it('me/university is 404 without membership; join creates ACTIVE/STUDENT/UNVERIFIED', async () => {
      const uni = await mkUniversity('Join University', 'join-uni');
      const me = await mkUser('joiner@example.com');
      await api()
        .get('/api/v1/users/me/university')
        .set(auth(me.token))
        .expect(404);
      const res = await joinUniversity(me.token, uni.id);
      expect(res.body.data.status).toBe('ACTIVE');
      expect(res.body.data.role).toBe('STUDENT');
      expect(res.body.data.verificationStatus).toBe('UNVERIFIED');
      expect(res.body.data.university.slug).toBe('join-uni');
      const me2 = await api()
        .get('/api/v1/users/me/university')
        .set(auth(me.token))
        .expect(200);
      expect(me2.body.data.university.id).toBe(uni.id);
    });

    it('duplicate join is idempotent; join of inactive university is 404', async () => {
      const uni = await mkUniversity('Idem University', 'idem-uni');
      const me = await mkUser('idem@example.com');
      await joinUniversity(me.token, uni.id);
      const again = await joinUniversity(me.token, uni.id);
      expect(again.body.data.status).toBe('ACTIVE');
      expect(
        await prisma.universityMembership.count({
          where: { userId: me.id, universityId: uni.id },
        }),
      ).toBe(1);
      const dead = await mkUniversity('Gone', 'gone-uni');
      await prisma.university.update({
        where: { id: dead.id },
        data: { isActive: false },
      });
      await joinUniversity(me.token, dead.id, 404);
    });

    it('switching primary suspends the previous ACTIVE membership', async () => {
      const a = await mkUniversity('Uni A', 'uni-a');
      const b = await mkUniversity('Uni B', 'uni-b');
      const me = await mkUser('switcher@example.com');
      await joinUniversity(me.token, a.id);
      await api()
        .patch('/api/v1/users/me/university')
        .set(auth(me.token))
        .send({ universityId: b.id })
        .expect(200);
      const mine = await api()
        .get('/api/v1/users/me/university')
        .set(auth(me.token))
        .expect(200);
      expect(mine.body.data.university.slug).toBe('uni-b');
      const rows = await prisma.universityMembership.findMany({
        where: { userId: me.id },
      });
      expect(rows.filter((r) => r.status === 'ACTIVE')).toHaveLength(1);
    });

    it('clients cannot self-award verification or roles', async () => {
      const uni = await mkUniversity('Strict University', 'strict-uni');
      const me = await mkUser('strict@example.com');
      await api()
        .post('/api/v1/users/me/university')
        .set(auth(me.token))
        .send({
          universityId: uni.id,
          verificationStatus: 'VERIFIED',
          role: 'ADMIN',
          status: 'ACTIVE',
        })
        .expect(400);
      const res = await joinUniversity(me.token, uni.id);
      expect(res.body.data.verificationStatus).toBe('UNVERIFIED');
      expect(res.body.data.role).toBe('STUDENT');
    });
  });

  // ------------------------------------------------------------- communities
  describe('communities', () => {
    it('creates in own university with OWNER membership; rejects foreign university', async () => {
      const mine = await mkUniversity('Mine', 'mine-uni');
      const theirs = await mkUniversity('Theirs', 'theirs-uni');
      const me = await mkUser('founder@example.com');
      await joinUniversity(me.token, mine.id);
      const res = await mkCommunity(me.token, {
        universityId: mine.id,
        name: 'Chess Club',
        slug: 'chess-club',
      });
      expect(res.body.data.slug).toBe('chess-club');
      expect(res.body.data.visibility).toBe('UNIVERSITY');
      await mkCommunity(
        me.token,
        { universityId: theirs.id, name: 'X', slug: 'x' },
        403,
      );
      const nomad = await mkUser('nomad@example.com');
      await mkCommunity(
        nomad.token,
        { universityId: mine.id, name: 'Y', slug: 'y' },
        403,
      );
    });

    it('slugs unique per university only; malformed slugs rejected', async () => {
      const a = await mkUniversity('Slug A', 'slug-a');
      const b = await mkUniversity('Slug B', 'slug-b');
      const ua = await mkUser('sa@example.com');
      const ub = await mkUser('sb@example.com');
      await joinUniversity(ua.token, a.id);
      await joinUniversity(ub.token, b.id);
      await mkCommunity(ua.token, {
        universityId: a.id,
        name: 'Gaming',
        slug: 'gaming',
      });
      await mkCommunity(
        ua.token,
        { universityId: a.id, name: 'Gaming 2', slug: 'gaming' },
        409,
      );
      await mkCommunity(ub.token, {
        universityId: b.id,
        name: 'Gaming',
        slug: 'gaming',
      }).then((r) => expect(r.status).toBe(201));
      await mkCommunity(
        ua.token,
        { universityId: a.id, name: 'Bad', slug: 'Bad Slug!' },
        400,
      );
    });

    it('visibility: PUBLIC cross-university visible; UNIVERSITY gated; PRIVATE members-only', async () => {
      const a = await mkUniversity('Vis A', 'vis-a');
      const b = await mkUniversity('Vis B', 'vis-b');
      const ua = await mkUser('va@example.com');
      const ub = await mkUser('vb@example.com');
      await joinUniversity(ua.token, a.id);
      await joinUniversity(ub.token, b.id);
      const pub = (
        await mkCommunity(ua.token, {
          universityId: a.id,
          name: 'Open',
          slug: 'open-c',
          visibility: 'PUBLIC',
        })
      ).body.data;
      const priv = (
        await mkCommunity(ua.token, {
          universityId: a.id,
          name: 'Secret',
          slug: 'secret-c',
          visibility: 'PRIVATE',
        })
      ).body.data;
      const uni = (
        await mkCommunity(ua.token, {
          universityId: a.id,
          name: 'Dept',
          slug: 'dept-c',
          visibility: 'UNIVERSITY',
        })
      ).body.data;
      await api()
        .get(`/api/v1/communities/${pub.id}`)
        .set(auth(ub.token))
        .expect(200);
      await api()
        .get(`/api/v1/communities/${uni.id}`)
        .set(auth(ub.token))
        .expect(403);
      await api()
        .get(`/api/v1/communities/${priv.id}`)
        .set(auth(ub.token))
        .expect(403);
    });

    it('update restricted to owner; deactivate hides community', async () => {
      const uni = await mkUniversity('Upd', 'upd-uni');
      const owner = await mkUser('owner@example.com');
      const member = await mkUser('memberx@example.com');
      await joinUniversity(owner.token, uni.id);
      await joinUniversity(member.token, uni.id);
      const c = (
        await mkCommunity(owner.token, {
          universityId: uni.id,
          name: 'Drama',
          slug: 'drama',
        })
      ).body.data;
      await api()
        .post(`/api/v1/communities/${c.id}/join`)
        .set(auth(member.token))
        .expect(200);
      await api()
        .patch(`/api/v1/communities/${c.id}`)
        .set(auth(member.token))
        .send({ description: 'hijack' })
        .expect(403);
      await api()
        .patch(`/api/v1/communities/${c.id}`)
        .set(auth(owner.token))
        .send({ description: 'Shakespeare society' })
        .expect(200);
      await api()
        .delete(`/api/v1/communities/${c.id}`)
        .set(auth(member.token))
        .expect(403);
      await api()
        .delete(`/api/v1/communities/${c.id}`)
        .set(auth(owner.token))
        .expect(200);
      await api()
        .get(`/api/v1/communities/${c.id}`)
        .set(auth(owner.token))
        .expect(404);
    });

    it('join rules: private invite-only; banned stays out; concurrent joins dedupe', async () => {
      const uni = await mkUniversity('Join', 'join-c-uni');
      const owner = await mkUser('cowner@example.com');
      const other = await mkUser('cother@example.com');
      await joinUniversity(owner.token, uni.id);
      await joinUniversity(other.token, uni.id);
      const priv = (
        await mkCommunity(owner.token, {
          universityId: uni.id,
          name: 'Priv',
          slug: 'priv-c',
          visibility: 'PRIVATE',
        })
      ).body.data;
      await api()
        .post(`/api/v1/communities/${priv.id}/join`)
        .set(auth(other.token))
        .expect(403);

      const open = (
        await mkCommunity(owner.token, {
          universityId: uni.id,
          name: 'Open2',
          slug: 'open2-c',
        })
      ).body.data;
      const results = await Promise.allSettled([
        api().post(`/api/v1/communities/${open.id}/join`).set(auth(other.token)),
        api().post(`/api/v1/communities/${open.id}/join`).set(auth(other.token)),
      ]);
      for (const r of results) {
        if (r.status !== 'fulfilled') throw new Error('request failed');
        expect(r.value.status).toBe(200);
      }
      expect(
        await prisma.communityMembership.count({
          where: { communityId: open.id, userId: other.id },
        }),
      ).toBe(1);
    });

    it('owner cannot leave; member leave allows rejoin; roles managed by owner', async () => {
      const uni = await mkUniversity('Roles', 'roles-uni');
      const owner = await mkUser('roleowner@example.com');
      const m1 = await mkUser('rolem1@example.com');
      const m2 = await mkUser('rolem2@example.com');
      for (const u of [owner, m1, m2]) await joinUniversity(u.token, uni.id);
      const c = (
        await mkCommunity(owner.token, {
          universityId: uni.id,
          name: 'Mods',
          slug: 'mods-c',
        })
      ).body.data;
      for (const u of [m1, m2]) {
        await api()
          .post(`/api/v1/communities/${c.id}/join`)
          .set(auth(u.token))
          .expect(200);
      }
      // Owner cannot leave.
      await api()
        .delete(`/api/v1/communities/${c.id}/leave`)
        .set(auth(owner.token))
        .expect(400);
      // Member leave + rejoin.
      await api()
        .delete(`/api/v1/communities/${c.id}/leave`)
        .set(auth(m1.token))
        .expect(200);
      await api()
        .post(`/api/v1/communities/${c.id}/join`)
        .set(auth(m1.token))
        .expect(200);
      // Promote m1 to moderator (owner only).
      await api()
        .patch(`/api/v1/communities/${c.id}/members/${m1.id}`)
        .set(auth(m2.token))
        .send({ role: 'MODERATOR' })
        .expect(403);
      await api()
        .patch(`/api/v1/communities/${c.id}/members/${m1.id}`)
        .set(auth(owner.token))
        .send({ role: 'MODERATOR' })
        .expect(200);
      // Owner role immutable here.
      await api()
        .patch(`/api/v1/communities/${c.id}/members/${owner.id}`)
        .set(auth(owner.token))
        .send({ role: 'MEMBER' })
        .expect(400);
      // Moderator removes a MEMBER → banned; rejoin refused.
      await api()
        .delete(`/api/v1/communities/${c.id}/members/${m2.id}`)
        .set(auth(m1.token))
        .expect(200);
      await api()
        .post(`/api/v1/communities/${c.id}/join`)
        .set(auth(m2.token))
        .expect(403);
      // Moderator cannot remove the owner.
      await api()
        .delete(`/api/v1/communities/${c.id}/members/${owner.id}`)
        .set(auth(m1.token))
        .expect(403);
    });

    it('members list respects privacy; includes roles', async () => {
      const uni = await mkUniversity('Members', 'members-uni');
      const owner = await mkUser('memowner@example.com');
      const m = await mkUser('memm@example.com');
      await joinUniversity(owner.token, uni.id);
      await joinUniversity(m.token, uni.id);
      const c = (
        await mkCommunity(owner.token, {
          universityId: uni.id,
          name: 'Roster',
          slug: 'roster-c',
          visibility: 'PRIVATE',
        })
      ).body.data;
      // Non-member cannot list a private community's members.
      await api()
        .get(`/api/v1/communities/${c.id}/members`)
        .set(auth(m.token))
        .expect(403);
      const res = await api()
        .get(`/api/v1/communities/${c.id}/members`)
        .set(auth(owner.token))
        .expect(200);
      expect(res.body.data.items).toHaveLength(1);
      expect(res.body.data.items[0].role).toBe('OWNER');
      expect(res.body.data.items[0].user.email).toBeUndefined();
    });
  });

  // ---------------------------------------------------------------- follow
  describe('follow graph', () => {
    it('follow/unfollow idempotent; self-follow and unknown users rejected', async () => {
      const a = await mkUser('folla@example.com');
      const b = await mkUser('follb@example.com');
      expect(
        (await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200))
          .body.data.following,
      ).toBe(true);
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
      expect(
        await prisma.follow.count({ where: { followerId: a.id, followingId: b.id } }),
      ).toBe(1);
      await api().post(`/api/v1/users/${a.id}/follow`).set(auth(a.token)).expect(400);
      await api().post(`/api/v1/users/00000000-0000-4000-8000-000000000000/follow`).set(auth(a.token)).expect(404);
      await api().delete(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
      await api().delete(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
    });

    it('followers/following lists are correct', async () => {
      const users = await mkUsers(3, 'flist');
      const [a, b, c] = users;
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(c.token)).expect(200);
      const followers = await api()
        .get(`/api/v1/users/${b.id}/followers`)
        .set(auth(a.token))
        .expect(200);
      expect(followers.body.data.items).toHaveLength(2);
      const following = await api()
        .get(`/api/v1/users/${a.id}/following`)
        .set(auth(a.token))
        .expect(200);
      expect(following.body.data.items).toHaveLength(1);
    });
  });

  // ----------------------------------------------------------------- block
  describe('block graph', () => {
    it('block severs follows both ways; blocked users cannot follow', async () => {
      const a = await mkUser('blocka@example.com');
      const b = await mkUser('blockb@example.com');
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
      await api().post(`/api/v1/users/${a.id}/block`).set(auth(b.token)).expect(200);
      // Follows severed.
      expect(await prisma.follow.count()).toBe(0);
      // Neither direction can follow now.
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(403);
      await api().post(`/api/v1/users/${a.id}/follow`).set(auth(b.token)).expect(403);
      // Duplicate block idempotent; self-block rejected; unblock works.
      await api().post(`/api/v1/users/${a.id}/block`).set(auth(b.token)).expect(200);
      await api().post(`/api/v1/users/${b.id}/block`).set(auth(b.token)).expect(400);
      await api().delete(`/api/v1/users/${a.id}/block`).set(auth(b.token)).expect(200);
      await api().post(`/api/v1/users/${b.id}/follow`).set(auth(a.token)).expect(200);
    });

    it('blocked users vanish from follower lists', async () => {
      const users = await mkUsers(3, 'blist');
      const [a, b, c] = users;
      await api().post(`/api/v1/users/${a.id}/follow`).set(auth(b.token)).expect(200);
      await api().post(`/api/v1/users/${a.id}/follow`).set(auth(c.token)).expect(200);
      await api().post(`/api/v1/users/${b.id}/block`).set(auth(a.token)).expect(200);
      const list = await api()
        .get(`/api/v1/users/${a.id}/followers`)
        .set(auth(c.token))
        .expect(200);
      const ids = list.body.data.items.map((i: { user: { id: string } }) => i.user.id);
      expect(ids).toContain(c.id);
      expect(ids).not.toContain(b.id);
    });
  });

  // ----------------------------------------------------------------- posts
  describe('posts', () => {
    async function setupPair() {
      const uni = await mkUniversity('Post Uni', `post-uni-${Date.now()}`);
      const author = await mkUser(`author${Date.now()}@example.com`);
      const viewer = await mkUser(`viewer${Date.now()}@example.com`);
      await joinUniversity(author.token, uni.id);
      await joinUniversity(viewer.token, uni.id);
      return { uni, author, viewer };
    }

    it('requires university membership; derives university server-side', async () => {
      const nomad = await mkUser('postnomad@example.com');
      await api()
        .post('/api/v1/posts')
        .set(auth(nomad.token))
        .send({ content: 'Hello campus' })
        .expect(403);
      // Ownership injection rejected by whitelist.
      const { uni, author } = await setupPair();
      await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: 'Hi', authorId: author.id, universityId: uni.id })
        .expect(400);
      const res = await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: 'Hello campus' })
        .expect(201);
      expect(res.body.data.universityId).toBe(uni.id);
      expect(res.body.data.visibility).toBe('UNIVERSITY');
      expect(res.body.data.likeCount).toBe(0);
    });

    it('community posts require membership; COMMUNITY needs a community', async () => {
      const { uni, author, viewer } = await setupPair();
      const c = (
        await mkCommunity(author.token, {
          universityId: uni.id,
          name: 'Study',
          slug: `study-${Date.now()}`,
        })
      ).body.data;
      await api()
        .post('/api/v1/posts')
        .set(auth(viewer.token))
        .send({ content: 'outsider', communityId: c.id })
        .expect(403);
      await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: 'no community', visibility: 'COMMUNITY' })
        .expect(400);
      const res = await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: 'members only', visibility: 'COMMUNITY', communityId: c.id })
        .expect(201);
      expect(res.body.data.communityId).toBe(c.id);
      await api().get(`/api/v1/posts/${res.body.data.id}`).set(auth(viewer.token)).expect(404);
    });

    it('author can update/delete; others cannot; deleted posts vanish', async () => {
      const { author, viewer } = await setupPair();
      const p = (
        await api()
          .post('/api/v1/posts')
          .set(auth(author.token))
          .send({ content: 'original', visibility: 'PUBLIC' })
          .expect(201)
      ).body.data;
      await api()
        .patch(`/api/v1/posts/${p.id}`)
        .set(auth(viewer.token))
        .send({ content: 'hijack' })
        .expect(403);
      await api()
        .patch(`/api/v1/posts/${p.id}`)
        .set(auth(author.token))
        .send({ content: 'edited' })
        .expect(200);
      await api()
        .delete(`/api/v1/posts/${p.id}`)
        .set(auth(viewer.token))
        .expect(403);
      await api().delete(`/api/v1/posts/${p.id}`).set(auth(author.token)).expect(200);
      await api().get(`/api/v1/posts/${p.id}`).set(auth(author.token)).expect(404);
      await api()
        .post(`/api/v1/posts/${p.id}/like`)
        .set(auth(viewer.token))
        .expect(404);
    });

    it('visibility: university isolation, followers-only, public', async () => {
      const uniA = await mkUniversity('Iso A', `iso-a-${Date.now()}`);
      const uniB = await mkUniversity('Iso B', `iso-b-${Date.now()}`);
      const a = await mkUser(`isoa${Date.now()}@example.com`);
      const b = await mkUser(`isob${Date.now()}@example.com`);
      await joinUniversity(a.token, uniA.id);
      await joinUniversity(b.token, uniB.id);
      const uni = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'uni secret' }).expect(201)
      ).body.data;
      await api().get(`/api/v1/posts/${uni.id}`).set(auth(b.token)).expect(404);
      await api().get(`/api/v1/universities/${uniB.slug}/posts`).set(auth(b.token)).expect(200);
      const listed = await api()
        .get(`/api/v1/universities/${uniA.slug}/posts`)
        .set(auth(b.token))
        .expect(403);
      expect(listed.body.error.code).toBe('FORBIDDEN');

      const pub = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'hello world', visibility: 'PUBLIC' }).expect(201)
      ).body.data;
      await api().get(`/api/v1/posts/${pub.id}`).set(auth(b.token)).expect(200);

      const fol = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'fans only', visibility: 'FOLLOWERS' }).expect(201)
      ).body.data;
      await api().get(`/api/v1/posts/${fol.id}`).set(auth(b.token)).expect(404);
      await api().post(`/api/v1/users/${a.id}/follow`).set(auth(b.token)).expect(200);
      await api().get(`/api/v1/posts/${fol.id}`).set(auth(b.token)).expect(200);
    });

    it('blocked authors are invisible and uninteractable', async () => {
      const uni = await mkUniversity('Blk Uni', `blk-${Date.now()}`);
      const a = await mkUser(`blka${Date.now()}@example.com`);
      const b = await mkUser(`blkb${Date.now()}@example.com`);
      await joinUniversity(a.token, uni.id);
      await joinUniversity(b.token, uni.id);
      const p = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'a post', visibility: 'PUBLIC' }).expect(201)
      ).body.data;
      await api().post(`/api/v1/users/${a.id}/block`).set(auth(b.token)).expect(200);
      await api().get(`/api/v1/posts/${p.id}`).set(auth(b.token)).expect(404);
      await api().post(`/api/v1/posts/${p.id}/like`).set(auth(b.token)).expect(404);
      await api().post(`/api/v1/posts/${p.id}/comments`).set(auth(b.token)).send({ content: 'hi' }).expect(404);
      const mine = await api()
        .get(`/api/v1/users/${a.id}/posts`)
        .set(auth(b.token))
        .expect(200);
      expect(mine.body.data.items).toHaveLength(0);
    });

    it('validation: oversized, empty, bad enum, bad UUID', async () => {
      const { author } = await setupPair();
      await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: 'x'.repeat(5001) })
        .expect(400);
      await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: '   ' })
        .expect(400);
      await api()
        .post('/api/v1/posts')
        .set(auth(author.token))
        .send({ content: 'hi', visibility: 'EVERYONE' })
        .expect(400);
      await api().get('/api/v1/posts/not-a-uuid').set(auth(author.token)).expect(400);
    });
  });

  // ----------------------------------------------------------------- likes
  describe('likes', () => {
    it('like/unlike idempotent with correct counts; concurrent likes dedupe', async () => {
      const uni = await mkUniversity('Like Uni', `like-${Date.now()}`);
      const a = await mkUser(`likea${Date.now()}@example.com`);
      const b = await mkUser(`likeb${Date.now()}@example.com`);
      await joinUniversity(a.token, uni.id);
      await joinUniversity(b.token, uni.id);
      const p = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'like me', visibility: 'PUBLIC' }).expect(201)
      ).body.data;
      const r1 = await api().post(`/api/v1/posts/${p.id}/like`).set(auth(b.token)).expect(200);
      expect(r1.body.data).toEqual({ liked: true, likeCount: 1 });
      const r2 = await api().post(`/api/v1/posts/${p.id}/like`).set(auth(b.token)).expect(200);
      expect(r2.body.data.likeCount).toBe(1);
      const r3 = await api().delete(`/api/v1/posts/${p.id}/like`).set(auth(b.token)).expect(200);
      expect(r3.body.data).toEqual({ liked: false, likeCount: 0 });
      await api().delete(`/api/v1/posts/${p.id}/like`).set(auth(b.token)).expect(200);

      // Concurrent double-like: single record, count 1.
      const results = await Promise.allSettled([
        api().post(`/api/v1/posts/${p.id}/like`).set(auth(b.token)),
        api().post(`/api/v1/posts/${p.id}/like`).set(auth(b.token)),
      ]);
      for (const r of results) {
        if (r.status !== 'fulfilled') throw new Error('request failed');
        expect(r.value.status).toBe(200);
      }
      expect(
        await prisma.like.count({ where: { userId: b.id, postId: p.id } }),
      ).toBe(1);
      const detail = await api().get(`/api/v1/posts/${p.id}`).set(auth(b.token)).expect(200);
      expect(detail.body.data.likeCount).toBe(1);
      expect(detail.body.data.likedByMe).toBe(true);
    });

    it('cannot like invisible or missing posts', async () => {
      const { author, viewer } = await (async () => {
        const uni = await mkUniversity('LikeG Uni', `likeg-${Date.now()}`);
        const author = await mkUser(`likega${Date.now()}@example.com`);
        const viewer = await mkUser(`likegv${Date.now()}@example.com`);
        await joinUniversity(author.token, uni.id);
        await joinUniversity(viewer.token, uni.id);
        return { author, viewer };
      })();
      const p = (
        await api().post('/api/v1/posts').set(auth(author.token)).send({ content: 'hidden gem' }).expect(201)
      ).body.data;
      // viewer shares the university so UNIVERSITY post is visible; use block to hide.
      await api().post(`/api/v1/users/${author.id}/block`).set(auth(viewer.token)).expect(200);
      await api().post(`/api/v1/posts/${p.id}/like`).set(auth(viewer.token)).expect(404);
      await api()
        .post('/api/v1/posts/00000000-0000-4000-8000-000000000000/like')
        .set(auth(viewer.token))
        .expect(404);
    });
  });

  // --------------------------------------------------------------- comments
  describe('comments', () => {
    it('create/list/update/delete with counts and tombstones', async () => {
      const uni = await mkUniversity('Cmt Uni', `cmt-${Date.now()}`);
      const a = await mkUser(`cmta${Date.now()}@example.com`);
      const b = await mkUser(`cmtb${Date.now()}@example.com`);
      await joinUniversity(a.token, uni.id);
      await joinUniversity(b.token, uni.id);
      const p = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'discuss', visibility: 'PUBLIC' }).expect(201)
      ).body.data;
      const c1 = (
        await api().post(`/api/v1/posts/${p.id}/comments`).set(auth(b.token)).send({ content: 'first!' }).expect(201)
      ).body.data;
      await api().post(`/api/v1/posts/${p.id}/comments`).set(auth(a.token)).send({ content: 'welcome' }).expect(201);
      const list = await api().get(`/api/v1/posts/${p.id}/comments`).set(auth(b.token)).expect(200);
      expect(list.body.data.items.map((c: { content: string }) => c.content)).toEqual([
        'first!',
        'welcome',
      ]);
      // Update own; cannot update another's (404, no probing).
      await api().patch(`/api/v1/comments/${c1.id}`).set(auth(b.token)).send({ content: 'first!!' }).expect(200);
      await api().patch(`/api/v1/comments/${c1.id}`).set(auth(a.token)).send({ content: 'hijack' }).expect(404);
      // Post owner can delete; comment disappears and count drops.
      await api().delete(`/api/v1/comments/${c1.id}`).set(auth(a.token)).expect(200);
      const after = await api().get(`/api/v1/posts/${p.id}/comments`).set(auth(b.token)).expect(200);
      expect(after.body.data.items.map((c: { content: string }) => c.content)).toEqual(['welcome']);
      const detail = await api().get(`/api/v1/posts/${p.id}`).set(auth(a.token)).expect(200);
      expect(detail.body.data.commentCount).toBe(1);
      // Repeat delete → 404.
      await api().delete(`/api/v1/comments/${c1.id}`).set(auth(a.token)).expect(404);
    });

    it('blocked users cannot comment; invisible posts reject comments', async () => {
      const uni = await mkUniversity('CmtB Uni', `cmtb-${Date.now()}`);
      const a = await mkUser(`cmtba${Date.now()}@example.com`);
      const b = await mkUser(`cmtbb${Date.now()}@example.com`);
      await joinUniversity(a.token, uni.id);
      await joinUniversity(b.token, uni.id);
      const p = (
        await api().post('/api/v1/posts').set(auth(a.token)).send({ content: 'mine', visibility: 'PUBLIC' }).expect(201)
      ).body.data;
      await api().post(`/api/v1/users/${a.id}/block`).set(auth(b.token)).expect(200);
      await api().post(`/api/v1/posts/${p.id}/comments`).set(auth(b.token)).send({ content: 'nope' }).expect(404);
      await api().get(`/api/v1/posts/${p.id}/comments`).set(auth(b.token)).expect(404);
    });
  });

  // ------------------------------------------------- community post listing
  describe('community post listing', () => {
    it('private community posts require membership', async () => {
      const uni = await mkUniversity('CP Uni', `cp-${Date.now()}`);
      const owner = await mkUser(`cpo${Date.now()}@example.com`);
      const outsider = await mkUser(`cpx${Date.now()}@example.com`);
      await joinUniversity(owner.token, uni.id);
      await joinUniversity(outsider.token, uni.id);
      const c = (
        await mkCommunity(owner.token, {
          universityId: uni.id,
          name: 'Inner Circle',
          slug: `inner-${Date.now()}`,
          visibility: 'PRIVATE',
        })
      ).body.data;
      await api()
        .post('/api/v1/posts')
        .set(auth(owner.token))
        .send({ content: 'inner thoughts', visibility: 'COMMUNITY', communityId: c.id })
        .expect(201);
      await api().get(`/api/v1/communities/${c.id}/posts`).set(auth(outsider.token)).expect(403);
      const mine = await api()
        .get(`/api/v1/communities/${c.id}/posts`)
        .set(auth(owner.token))
        .expect(200);
      expect(mine.body.data.items).toHaveLength(1);
    });
  });
});

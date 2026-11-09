import { INestApplication } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import { Test, TestingModule } from '@nestjs/testing';
import { argon2id, hash } from 'argon2';
import { randomUUID } from 'crypto';
import request from 'supertest';
import type { Response } from 'supertest';
import { AppModule } from '../src/app.module';
import { PrismaService } from '../src/config/prisma.service';
import { EmailService } from '../src/common/email/email.service';
import { REDIS_CLIENT } from '../src/common/redis/redis.module';
import type Redis from 'ioredis';

const PASSWORD = 'Str0ng-Passw0rd!';
const DAY = 24 * 60 * 60 * 1000;

describe('Slice 3: events + free ticketing', () => {
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

  /** Fixture user via DB (avoids auth rate limits); token minted directly. */
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

  async function mkUniversity(slug: string, name?: string) {
    return prisma.university.create({ data: { name: name ?? slug, slug } });
  }

  async function join(token: string, universityId: string, expected = 200) {
    return api()
      .post('/api/v1/users/me/university')
      .set(auth(token))
      .send({ universityId })
      .expect(expected);
  }

  function eventBody(over: Record<string, unknown> = {}) {
    const start = new Date(Date.now() + 7 * DAY).toISOString();
    const end = new Date(Date.now() + 7 * DAY + 3600_000).toISOString();
    return {
      title: 'Campus Hack Night',
      description: 'Build things together.',
      type: 'MEETUP',
      location: 'Innovation Hub',
      startsAt: start,
      endsAt: end,
      capacity: 50,
      visibility: 'UNIVERSITY',
      ...over,
    };
  }

  async function mkEvent(
    token: string,
    over: Record<string, unknown> = {},
    publish = true,
  ) {
    const created = await api()
      .post('/api/v1/events')
      .set(auth(token))
      .send(eventBody(over))
      .expect(201);
    const id = created.body.data.id as string;
    if (publish) {
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(token))
        .send({ status: 'PUBLISHED' })
        .expect(200);
    }
    return id;
  }

  async function setupCampus(prefix: string) {
    const uni = await mkUniversity(`${prefix}-uni`);
    const organizer = await mkUser(`${prefix}-org@example.com`);
    const attendee = await mkUser(`${prefix}-att@example.com`);
    await join(organizer.token, uni.id);
    await join(attendee.token, uni.id);
    return { uni, organizer, attendee };
  }

  // ---------------------------------------------------------- event creation
  describe('event creation', () => {
    it('requires membership, derives university, starts DRAFT', async () => {
      const uni = await mkUniversity('crt-uni');
      const nomad = await mkUser('nomad3@example.com');
      await api()
        .post('/api/v1/events')
        .set(auth(nomad.token))
        .send(eventBody())
        .expect(403);
      const org = await mkUser('org3@example.com');
      await join(org.token, uni.id);
      const res = await api()
        .post('/api/v1/events')
        .set(auth(org.token))
        .send(eventBody())
        .expect(201);
      expect(res.body.data.status).toBe('DRAFT');
      expect(res.body.data.universityId).toBe(uni.id);
      expect(res.body.data.registeredCount).toBe(0);
      expect(res.body.data.availableSpots).toBe(50);
    });

    it('validates dates, capacity, enums, UUIDs', async () => {
      const { organizer } = await setupCampus('evval');
      const bad = [
        { endsAt: new Date(Date.now() + DAY).toISOString(), startsAt: new Date(Date.now() + 2 * DAY).toISOString() },
        { registrationDeadline: new Date(Date.now() + 8 * DAY).toISOString() },
        { capacity: 0 },
        { capacity: 100001 },
        { capacity: 'many' },
        { visibility: 'EVERYONE' },
        { type: 'PARTY' },
        { communityId: 'not-a-uuid' },
        { title: '' },
        { title: 'x'.repeat(201) },
        { startsAt: 'next friday-ish' },
      ];
      for (const over of bad) {
        await api()
          .post('/api/v1/events')
          .set(auth(organizer.token))
          .send(eventBody(over))
          .expect(400);
      }
    });

    it('rejects organizerId/status injection; community must match', async () => {
      const { uni, organizer } = await setupCampus('evinj');
      await api()
        .post('/api/v1/events')
        .set(auth(organizer.token))
        .send({ ...eventBody(), organizerId: organizer.id, status: 'PUBLISHED', registeredCount: 99 })
        .expect(400);
      const other = await mkUniversity('evinj-other');
      const comm = await prisma.community.create({
        data: { universityId: other.id, name: 'Foreign', slug: 'foreign' },
      });
      await api()
        .post('/api/v1/events')
        .set(auth(organizer.token))
        .send(eventBody({ communityId: comm.id }))
        .expect(403);
      // COMMUNITY visibility without community.
      await api()
        .post('/api/v1/events')
        .set(auth(organizer.token))
        .send(eventBody({ visibility: 'COMMUNITY' }))
        .expect(400);
      // Community membership required.
      const mine = await prisma.community.create({
        data: { universityId: uni.id, name: 'Mine', slug: 'mine-c' },
      });
      await api()
        .post('/api/v1/events')
        .set(auth(organizer.token))
        .send(eventBody({ communityId: mine.id }))
        .expect(403);
    });
  });

  // --------------------------------------------------------------- lifecycle
  describe('event lifecycle', () => {
    it('draft hidden from others; publish/complete/cancel enforced', async () => {
      const { organizer, attendee } = await setupCampus('life');
      const id = await mkEvent(organizer.token, {}, false);
      await api().get(`/api/v1/events/${id}`).set(auth(attendee.token)).expect(404);
      // Invalid transition DRAFT -> COMPLETED.
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(organizer.token))
        .send({ status: 'COMPLETED' })
        .expect(400);
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(organizer.token))
        .send({ status: 'PUBLISHED' })
        .expect(200);
      await api().get(`/api/v1/events/${id}`).set(auth(attendee.token)).expect(200);
      // Non-organizer cannot mutate lifecycle.
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(attendee.token))
        .send({ title: 'hijack' })
        .expect(403);
      await api().delete(`/api/v1/events/${id}`).set(auth(attendee.token)).expect(403);
      // Capacity cannot shrink below registrations.
      await api()
        .post(`/api/v1/events/${id}/register`)
        .set(auth(attendee.token))
        .expect(200);
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(organizer.token))
        .send({ capacity: 0 })
        .expect(400);
      // Complete, then terminal.
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(organizer.token))
        .send({ status: 'COMPLETED' })
        .expect(200);
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(organizer.token))
        .send({ title: 'late edit' })
        .expect(400);
      await api().delete(`/api/v1/events/${id}`).set(auth(organizer.token)).expect(400);
    });

    it('cancel blocks registration; cancelled visible to holders only', async () => {
      const { organizer, attendee } = await setupCampus('cancel');
      const outsider = await mkUser('cancel-out@example.com');
      const uni = await prisma.university.findFirstOrThrow({ where: { slug: 'cancel-uni' } });
      await join(outsider.token, uni.id);
      const id = await mkEvent(organizer.token);
      await api().post(`/api/v1/events/${id}/register`).set(auth(attendee.token)).expect(200);
      await api().delete(`/api/v1/events/${id}`).set(auth(organizer.token)).expect(200);
      // Cancel is idempotent.
      await api().delete(`/api/v1/events/${id}`).set(auth(organizer.token)).expect(200);
      const late = await mkUser('cancel-late@example.com');
      await join(late.token, uni.id);
      await api().post(`/api/v1/events/${id}/register`).set(auth(late.token)).expect(409);
      // Holder still sees it; outsider gets 404.
      await api().get(`/api/v1/events/${id}`).set(auth(attendee.token)).expect(200);
      await api().get(`/api/v1/events/${id}`).set(auth(outsider.token)).expect(404);
    });
  });

  // -------------------------------------------------------------- visibility
  describe('event visibility + discovery', () => {
    it('PUBLIC cross-university; UNIVERSITY/COMMUNITY gated; blocked hidden', async () => {
      const a = await mkUniversity('vis3-a');
      const b = await mkUniversity('vis3-b');
      const org = await mkUser('vis3org@example.com');
      const same = await mkUser('vis3same@example.com');
      const other = await mkUser('vis3other@example.com');
      await join(org.token, a.id);
      await join(same.token, a.id);
      await join(other.token, b.id);

      const pub = await mkEvent(org.token, { title: 'Open Day', visibility: 'PUBLIC' });
      await api().get(`/api/v1/events/${pub}`).set(auth(other.token)).expect(200);

      const uni = await mkEvent(org.token, { title: 'Uni Only' });
      await api().get(`/api/v1/events/${uni}`).set(auth(other.token)).expect(404);
      await api().get(`/api/v1/universities/${a.slug}/events`).set(auth(other.token)).expect(403);

      const comm = await prisma.community.create({
        data: { universityId: a.id, name: 'Team', slug: 'team-e' },
      });
      await prisma.communityMembership.create({
        data: { userId: org.id, communityId: comm.id, role: 'OWNER', status: 'ACTIVE' },
      });
      const cev = await mkEvent(org.token, {
        title: 'Team Sync',
        visibility: 'COMMUNITY',
        communityId: comm.id,
      });
      await api().get(`/api/v1/events/${cev}`).set(auth(same.token)).expect(404);
      await api().get(`/api/v1/communities/${comm.id}/events`).set(auth(same.token)).expect(200);

      // Block hides even PUBLIC events.
      await api().post(`/api/v1/users/${org.id}/block`).set(auth(other.token)).expect(200);
      await api().get(`/api/v1/events/${pub}`).set(auth(other.token)).expect(404);
    });

    it('listing filters: mine shows drafts; DRAFT without mine is 400', async () => {
      const { organizer } = await setupCampus('list3');
      await mkEvent(organizer.token, { title: 'Published One' });
      await mkEvent(organizer.token, { title: 'Draft One' }, false);
      const mine = await api()
        .get('/api/v1/events?mine=true')
        .set(auth(organizer.token))
        .expect(200);
      expect(mine.body.data.items).toHaveLength(2);
      const pub = await api()
        .get('/api/v1/events')
        .set(auth(organizer.token))
        .expect(200);
      expect(pub.body.data.items).toHaveLength(1);
      await api()
        .get('/api/v1/events?status=DRAFT')
        .set(auth(organizer.token))
        .expect(400);
      const upcoming = await api()
        .get('/api/v1/events?upcoming=true')
        .set(auth(organizer.token))
        .expect(200);
      expect(upcoming.body.data.items).toHaveLength(1);
    });
  });

  // ---------------------------------------------------------- event posts
  describe('event posts', () => {
    it('references by ID with summary; no data duplication', async () => {
      const { organizer, attendee } = await setupCampus('epost');
      const id = await mkEvent(organizer.token, { title: 'Robotics Night' });
      const res = await api()
        .post('/api/v1/posts')
        .set(auth(organizer.token))
        .send({ content: 'Come through!', visibility: 'PUBLIC', eventId: id })
        .expect(201);
      expect(res.body.data.event).toMatchObject({ id, title: 'Robotics Night', status: 'PUBLISHED' });
      expect(res.body.data.title).toBeUndefined();
      expect(res.body.data.startsAt).toBeUndefined();
      const fetched = await api()
        .get(`/api/v1/posts/${res.body.data.id}`)
        .set(auth(attendee.token))
        .expect(200);
      expect(fetched.body.data.event.id).toBe(id);
    });

    it('rejects incompatible/missing/draft events; summary hidden when unauthorized', async () => {
      const uniA = await mkUniversity('ep-a');
      const uniB = await mkUniversity('ep-b');
      const a = await mkUser('epa@example.com');
      const b = await mkUser('epb@example.com');
      await join(a.token, uniA.id);
      await join(b.token, uniB.id);
      const ev = await mkEvent(a.token, { title: 'Local Gig' });
      // Cross-university reference rejected.
      await api()
        .post('/api/v1/posts')
        .set(auth(b.token))
        .send({ content: 'x', eventId: ev })
        .expect(403);
      // Missing event.
      await api()
        .post('/api/v1/posts')
        .set(auth(a.token))
        .send({ content: 'x', eventId: '00000000-0000-4000-8000-000000000000' })
        .expect(404);
      // Draft event.
      const draft = await mkEvent(a.token, { title: 'Secret' }, false);
      await api()
        .post('/api/v1/posts')
        .set(auth(a.token))
        .send({ content: 'x', eventId: draft })
        .expect(400);
      // PUBLIC post + COMMUNITY event: post visible, summary gated.
      const comm = await prisma.community.create({
        data: { universityId: uniA.id, name: 'Crew', slug: 'crew-e' },
      });
      await prisma.communityMembership.create({
        data: { userId: a.id, communityId: comm.id, role: 'OWNER', status: 'ACTIVE' },
      });
      const cev = await mkEvent(a.token, {
        title: 'Crew Only',
        visibility: 'COMMUNITY',
        communityId: comm.id,
      });
      const p = (
        await api()
          .post('/api/v1/posts')
          .set(auth(a.token))
          .send({ content: 'shout', visibility: 'PUBLIC', eventId: cev })
          .expect(201)
      ).body.data;
      await join(b.token, uniA.id); // b joins uniA but not the community
      const seen = await api()
        .get(`/api/v1/posts/${p.id}`)
        .set(auth(b.token))
        .expect(200);
      expect(seen.body.data.event).toBeNull();
      const own = await api()
        .get(`/api/v1/posts/${p.id}`)
        .set(auth(a.token))
        .expect(200);
      expect(own.body.data.event).not.toBeNull();
    });
  });

  // ------------------------------------------------------------ registration
  describe('registration', () => {
    it('success, idempotent duplicate, cancel frees capacity', async () => {
      const { organizer, attendee } = await setupCampus('reg3');
      const id = await mkEvent(organizer.token, { capacity: 2 });
      const r1 = await api()
        .post(`/api/v1/events/${id}/register`)
        .set(auth(attendee.token))
        .expect(200);
      expect(r1.body.data.ticket.ticketCode).toMatch(/^[A-Za-z0-9_-]+$/);
      expect(r1.body.data.created).toBe(true);
      const r2 = await api()
        .post(`/api/v1/events/${id}/register`)
        .set(auth(attendee.token))
        .expect(200);
      expect(r2.body.data.created).toBe(false);
      expect(r2.body.data.ticket.id).toBe(r1.body.data.ticket.id);
      expect(
        await prisma.ticket.count({ where: { eventId: id, userId: attendee.id } }),
      ).toBe(1);
      const detail = await api().get(`/api/v1/events/${id}`).set(auth(attendee.token)).expect(200);
      expect(detail.body.data.registeredCount).toBe(1);
      expect(detail.body.data.myTicket.status).toBe('ACTIVE');
      // Cancel then re-register works.
      await api().delete(`/api/v1/events/${id}/registration`).set(auth(attendee.token)).expect(200);
      await api().delete(`/api/v1/events/${id}/registration`).set(auth(attendee.token)).expect(404);
      const r3 = await api()
        .post(`/api/v1/events/${id}/register`)
        .set(auth(attendee.token))
        .expect(200);
      expect(r3.body.data.ticket.status).toBe('ACTIVE');
    });

    it('rejects full/deadline/closed/private states with clean codes', async () => {
      const uni = await mkUniversity('regx-uni');
      const org = await mkUser('regxorg@example.com');
      const a1 = await mkUser('regxa1@example.com');
      const a2 = await mkUser('regxa2@example.com');
      for (const u of [org, a1, a2]) await join(u.token, uni.id);
      const full = await mkEvent(org.token, { capacity: 1 });
      await api().post(`/api/v1/events/${full}/register`).set(auth(a1.token)).expect(200);
      const res = await api()
        .post(`/api/v1/events/${full}/register`)
        .set(auth(a2.token))
        .expect(409);
      expect(res.body.error.code).toBe('EVENT_FULL');

      const past = await mkEvent(org.token, {
        registrationDeadline: new Date(Date.now() - 1000).toISOString(),
      });
      const dl = await api()
        .post(`/api/v1/events/${past}/register`)
        .set(auth(a1.token))
        .expect(409);
      expect(dl.body.error.code).toBe('REGISTRATION_CLOSED');

      const draft = await mkEvent(org.token, {}, false);
      await api().post(`/api/v1/events/${draft}/register`).set(auth(a1.token)).expect(409);

      await api()
        .patch(`/api/v1/events/${draft}`)
        .set(auth(org.token))
        .send({ status: 'CANCELLED' })
        .expect(200);
      await api().post(`/api/v1/events/${draft}/register`).set(auth(a1.token)).expect(409);

      // Blocked attendee cannot register even for PUBLIC events.
      const pub = await mkEvent(org.token, { visibility: 'PUBLIC' });
      await api().post(`/api/v1/users/${org.id}/block`).set(auth(a2.token)).expect(200);
      await api().post(`/api/v1/events/${pub}/register`).set(auth(a2.token)).expect(403);
    });

    it('capacity race: 5 users, 3 slots → exactly 3 tickets', async () => {
      const uni = await mkUniversity('race-uni');
      const org = await mkUser('raceorg@example.com');
      await join(org.token, uni.id);
      const racers: { id: string; token: string }[] = [];
      for (let i = 0; i < 5; i++) {
        const u = await prisma.user.create({
          data: { email: `racer${i}@example.com`, passwordHash: 'x' },
        });
        await prisma.universityMembership.create({
          data: { userId: u.id, universityId: uni.id, status: 'ACTIVE' },
        });
        racers.push({ id: u.id, token: 'unused' });
      }
      // Mint real access tokens directly (avoids auth rate limits in bursts).
      const tokens = racers.map((r) =>
        (jwt as JwtService).sign(
          { sub: r.id, role: 'STUDENT', jti: randomUUID() },
          { secret: config.get<string>('jwt.accessSecret'), expiresIn: 900 },
        ),
      );
      const id = await mkEvent(org.token, { capacity: 3 });
      const results = await Promise.allSettled(
        tokens.map((t) =>
          api().post(`/api/v1/events/${id}/register`).set(auth(t)),
        ),
      );
      const ok = results.filter(
        (r): r is PromiseFulfilledResult<Response> =>
          r.status === 'fulfilled' && r.value.status === 200,
      );
      expect(ok).toHaveLength(3);
      for (const r of results) {
        if (r.status === 'fulfilled' && r.value.status !== 200) {
          expect(r.value.status).toBe(409);
          expect(r.value.body.error.code).toBe('EVENT_FULL');
        } else {
          expect(r.status).toBe('fulfilled');
        }
      }
      expect(await prisma.ticket.count({ where: { eventId: id, status: 'ACTIVE' } })).toBe(3);
      const ev = await prisma.event.findUniqueOrThrow({ where: { id } });
      expect(ev.registeredCount).toBe(3);
    });

    it('duplicate registration race yields exactly one ticket row', async () => {
      const { organizer, attendee } = await setupCampus('dupreg');
      const id = await mkEvent(organizer.token, { capacity: 10 });
      const results = await Promise.allSettled([
        api().post(`/api/v1/events/${id}/register`).set(auth(attendee.token)),
        api().post(`/api/v1/events/${id}/register`).set(auth(attendee.token)),
        api().post(`/api/v1/events/${id}/register`).set(auth(attendee.token)),
      ]);
      for (const r of results) {
        if (r.status !== 'fulfilled') throw new Error('request failed');
        expect(r.value.status).toBe(200);
      }
      expect(
        await prisma.ticket.count({ where: { eventId: id, userId: attendee.id } }),
      ).toBe(1);
      const ev2 = await prisma.event.findUniqueOrThrow({ where: { id } });
      expect(ev2.registeredCount).toBe(1);
    });
  });

  // --------------------------------------------------------------- tickets
  describe('tickets', () => {
    it('my tickets scoped to self; detail IDOR-safe; organizer sees attendee', async () => {
      const { organizer, attendee } = await setupCampus('tick');
      const other = await mkUser('tickother@example.com');
      const uni = await prisma.university.findFirstOrThrow({ where: { slug: 'tick-uni' } });
      await join(other.token, uni.id);
      const id = await mkEvent(organizer.token);
      const reg = await api()
        .post(`/api/v1/events/${id}/register`)
        .set(auth(attendee.token))
        .expect(200);
      const ticketId = reg.body.data.ticket.id as string;
      const code = reg.body.data.ticket.ticketCode as string;
      expect(code).toHaveLength(43);

      const mine = await api()
        .get('/api/v1/users/me/tickets')
        .set(auth(attendee.token))
        .expect(200);
      expect(mine.body.data.items).toHaveLength(1);
      expect(mine.body.data.items[0].ticketCode).toBe(code);
      const theirs = await api()
        .get('/api/v1/users/me/tickets')
        .set(auth(other.token))
        .expect(200);
      expect(theirs.body.data.items).toHaveLength(0);

      // Stranger cannot read the ticket; organizer can (no secrets leaked).
      await api().get(`/api/v1/tickets/${ticketId}`).set(auth(other.token)).expect(404);
      const org = await api()
        .get(`/api/v1/tickets/${ticketId}`)
        .set(auth(organizer.token))
        .expect(200);
      expect(org.body.data.attendee.id).toBe(attendee.id);
      expect(org.body.data.passwordHash).toBeUndefined();
      expect(JSON.stringify(org.body.data)).not.toContain('passwordHash');
    });

    it('verify: valid, repeat 409, wrong event 404, unauthorized 403', async () => {
      const { organizer, attendee } = await setupCampus('verify');
      const other = await mkUser('verifyother@example.com');
      const uni = await prisma.university.findFirstOrThrow({ where: { slug: 'verify-uni' } });
      await join(other.token, uni.id);
      const id = await mkEvent(organizer.token);
      const other2 = await mkEvent(organizer.token, { title: 'Second Night' });
      const code = (
        await api().post(`/api/v1/events/${id}/register`).set(auth(attendee.token)).expect(200)
      ).body.data.ticket.ticketCode as string;

      // Non-organizer cannot verify.
      await api()
        .post(`/api/v1/events/${id}/tickets/verify`)
        .set(auth(other.token))
        .send({ ticketCode: code })
        .expect(403);
      // Cross-event code rejected without leaking.
      await api()
        .post(`/api/v1/events/${other2}/tickets/verify`)
        .set(auth(organizer.token))
        .send({ ticketCode: code })
        .expect(404);
      // Unknown code.
      await api()
        .post(`/api/v1/events/${id}/tickets/verify`)
        .set(auth(organizer.token))
        .send({ ticketCode: 'x'.repeat(43) })
        .expect(404);
      // Valid check-in.
      const ok = await api()
        .post(`/api/v1/events/${id}/tickets/verify`)
        .set(auth(organizer.token))
        .send({ ticketCode: code })
        .expect(200);
      expect(ok.body.data.checkedIn).toBe(true);
      // Repeat → conflict.
      await api()
        .post(`/api/v1/events/${id}/tickets/verify`)
        .set(auth(organizer.token))
        .send({ ticketCode: code })
        .expect(409);
      // Cancelled ticket cannot check in.
      const c2 = (
        await api().post(`/api/v1/events/${id}/register`).set(auth(other.token)).expect(200)
      ).body.data.ticket.ticketCode as string;
      await api().delete(`/api/v1/events/${id}/registration`).set(auth(other.token)).expect(200);
      await api()
        .post(`/api/v1/events/${id}/tickets/verify`)
        .set(auth(organizer.token))
        .send({ ticketCode: c2 })
        .expect(409);
    });

    it('concurrent check-in yields exactly one success', async () => {
      const { organizer, attendee } = await setupCampus('concur');
      const id = await mkEvent(organizer.token);
      const code = (
        await api().post(`/api/v1/events/${id}/register`).set(auth(attendee.token)).expect(200)
      ).body.data.ticket.ticketCode as string;
      const results = await Promise.allSettled(
        Array.from({ length: 4 }, () =>
          api()
            .post(`/api/v1/events/${id}/tickets/verify`)
            .set(auth(organizer.token))
            .send({ ticketCode: code }),
        ),
      );
      const ok = results.filter(
        (r): r is PromiseFulfilledResult<Response> =>
          r.status === 'fulfilled' && r.value.status === 200,
      );
      expect(ok).toHaveLength(1);
      const t = await prisma.ticket.findFirstOrThrow({
        where: { event: { id }, userId: attendee.id },
      });
      expect(t.status).toBe('CHECKED_IN');
      expect(t.checkedInAt).not.toBeNull();
    });
  });

  // ------------------------------------------------- hostile event payloads
  describe('hostile payloads', () => {
    it('rejects ownership/count/status injection and bad enums', async () => {
      const { organizer } = await setupCampus('hostile');
      const id = await mkEvent(organizer.token);
      await api()
        .patch(`/api/v1/events/${id}`)
        .set(auth(organizer.token))
        .send({ organizerId: organizer.id, registeredCount: 999, universityId: 'x' })
        .expect(400);
      await api()
        .post(`/api/v1/events`)
        .set(auth(organizer.token))
        .send({ ...eventBody(), type: 'RAVE' })
        .expect(400);
      await api()
        .get('/api/v1/events?status=BOGUS')
        .set(auth(organizer.token))
        .expect(400);
    });
  });
});

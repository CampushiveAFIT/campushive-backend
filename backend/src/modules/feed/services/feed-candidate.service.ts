import { Injectable } from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../../config/prisma.service';
import { FEED_CANDIDATE_LIMIT } from '../types/feed.types';

const AUTHOR_SELECT = { id: true, displayName: true, avatarUrl: true } as const;

const POST_INCLUDE = {
  author: { select: AUTHOR_SELECT },
  event: {
    select: {
      id: true,
      organizerId: true,
      universityId: true,
      communityId: true,
      title: true,
      startsAt: true,
      endsAt: true,
      location: true,
      status: true,
      visibility: true,
    },
  },
} as const;

const EVENT_INCLUDE = {
  organizer: { select: AUTHOR_SELECT },
} as const;

export type FeedPostRow = Prisma.PostGetPayload<{ include: typeof POST_INCLUDE }>;
export type FeedEventRow = Prisma.EventGetPayload<{ include: typeof EVENT_INCLUDE }>;

/**
 * Candidate Retrieval: raw Prisma queries only, no authorization.
 * Every candidate is safety-filtered downstream by FeedVisibilityService.
 * Per-source caps keep feed assembly bounded (see FEED_CANDIDATE_LIMIT).
 */
@Injectable()
export class FeedCandidateService {
  constructor(private readonly prisma: PrismaService) {}

  private postOrder() {
    return [{ createdAt: 'desc' as const }, { id: 'desc' as const }];
  }

  async postsWhere(
    where: Prisma.PostWhereInput,
    limit = FEED_CANDIDATE_LIMIT,
  ): Promise<FeedPostRow[]> {
    return this.prisma.post.findMany({
      where: { ...where, deletedAt: null },
      include: POST_INCLUDE,
      orderBy: this.postOrder(),
      take: limit,
    });
  }

  async eventsWhere(
    where: Prisma.EventWhereInput,
    limit = FEED_CANDIDATE_LIMIT,
  ): Promise<FeedEventRow[]> {
    return this.prisma.event.findMany({
      where: { ...where, status: 'PUBLISHED' },
      include: EVENT_INCLUDE,
      orderBy: [{ createdAt: 'desc' as const }, { id: 'desc' as const }],
      take: limit,
    });
  }

  /** Posts by specific authors (followed users + self). */
  postsByAuthors(authorIds: string[]): Promise<FeedPostRow[]> {
    if (authorIds.length === 0) return Promise.resolve([]);
    return this.postsWhere({ authorId: { in: authorIds } });
  }

  /** Posts scoped to universities (any visibility; filtered later). */
  postsForUniversities(universityIds: string[]): Promise<FeedPostRow[]> {
    if (universityIds.length === 0) return Promise.resolve([]);
    return this.postsWhere({ universityId: { in: universityIds } });
  }

  /** Posts scoped to communities. */
  postsForCommunities(communityIds: string[]): Promise<FeedPostRow[]> {
    if (communityIds.length === 0) return Promise.resolve([]);
    return this.postsWhere({ communityId: { in: communityIds } });
  }

  /** Platform-wide public posts (Explore + Home public slice). */
  publicPosts(): Promise<FeedPostRow[]> {
    return this.postsWhere({ visibility: 'PUBLIC' }, 100);
  }

  /** Published events scoped to universities. */
  eventsForUniversities(universityIds: string[]): Promise<FeedEventRow[]> {
    if (universityIds.length === 0) return Promise.resolve([]);
    return this.eventsWhere({ universityId: { in: universityIds } });
  }

  /** Published events scoped to communities. */
  eventsForCommunities(communityIds: string[]): Promise<FeedEventRow[]> {
    if (communityIds.length === 0) return Promise.resolve([]);
    return this.eventsWhere({ communityId: { in: communityIds } });
  }

  /** Platform-wide public published events (Explore). */
  publicEvents(): Promise<FeedEventRow[]> {
    return this.eventsWhere({ visibility: 'PUBLIC' }, 100);
  }
}

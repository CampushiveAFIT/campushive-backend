import { BadRequestException, Injectable } from '@nestjs/common';
import { PrismaService } from '../../../config/prisma.service';
import { ApiErrorCode } from '../../../common/types/api-response';
import { loadViewerContext } from '../../../common/access/viewer-context';
import { FeedVisibilityService } from './feed-visibility.service';
import { SearchType } from '../dto/feed-query.dto';

export interface SearchResults {
  users: Array<{ id: string; displayName: string | null; avatarUrl: string | null }>;
  universities: Array<{
    id: string;
    name: string;
    slug: string;
    description: string | null;
    logoUrl: string | null;
  }>;
  communities: Array<{
    id: string;
    universityId: string;
    name: string;
    slug: string;
    description: string | null;
    type: string;
    visibility: string;
  }>;
  events: Array<{
    id: string;
    title: string;
    startsAt: Date;
    status: string;
    visibility: string;
  }>;
  posts: Array<{ id: string; content: string; createdAt: Date }>;
}

const DEFAULT_LIMIT = 10;
const MAX_LIMIT = 25;

/**
 * Basic substring search (case-insensitive) across the five Slice 2/3
 * domains. No search engine: Prisma ILIKE queries with tight per-type caps.
 * Visibility rules apply to posts/events/communities exactly as in feeds.
 */
@Injectable()
export class SearchService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly visibility: FeedVisibilityService,
  ) {}

  async search(
    viewerId: string,
    rawQuery: string,
    rawType: string | undefined,
    rawLimit: number | undefined,
  ): Promise<SearchResults> {
    const q = rawQuery.trim();
    if (q.length < 2) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Search query must be at least 2 characters.',
      });
    }
    const type = rawType ?? 'all';
    const valid = ['all', ...Object.values(SearchType)];
    if (!valid.includes(type)) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Invalid search type.',
      });
    }
    const limit =
      rawLimit === undefined ? DEFAULT_LIMIT : Math.min(Math.max(1, rawLimit), MAX_LIMIT);
    const ctx = await loadViewerContext(this.prisma, viewerId);
    const empty: SearchResults = {
      users: [],
      universities: [],
      communities: [],
      events: [],
      posts: [],
    };
    const want = (t: string) => type === 'all' || type === t;

    if (want(SearchType.USERS)) {
      const users = await this.prisma.user.findMany({
        where: {
          isActive: true,
          displayName: { contains: q, mode: 'insensitive' },
        },
        select: { id: true, displayName: true, avatarUrl: true },
        orderBy: { displayName: 'asc' },
        take: limit,
      });
      // Never expose blocked users in search results.
      empty.users = users.filter((u) => !ctx.blockedIds.includes(u.id));
    }
    if (want(SearchType.UNIVERSITIES)) {
      empty.universities = await this.prisma.university.findMany({
        where: {
          isActive: true,
          OR: [
            { name: { contains: q, mode: 'insensitive' } },
            { description: { contains: q, mode: 'insensitive' } },
          ],
        },
        select: {
          id: true,
          name: true,
          slug: true,
          description: true,
          logoUrl: true,
        },
        orderBy: { name: 'asc' },
        take: limit,
      });
    }
    if (want(SearchType.COMMUNITIES)) {
      const communities = await this.prisma.community.findMany({
        where: {
          isActive: true,
          OR: [
            { name: { contains: q, mode: 'insensitive' } },
            { description: { contains: q, mode: 'insensitive' } },
          ],
        },
        select: {
          id: true,
          universityId: true,
          name: true,
          slug: true,
          description: true,
          type: true,
          visibility: true,
        },
        orderBy: { name: 'asc' },
        take: limit,
      });
      empty.communities = communities.filter((c) => {
        if (c.visibility === 'PUBLIC') return true;
        if (c.visibility === 'UNIVERSITY') {
          return ctx.universityIds.includes(c.universityId);
        }
        // PRIVATE: members only.
        return ctx.communityIds.includes(c.id);
      });
    }
    if (want(SearchType.EVENTS)) {
      const events = await this.prisma.event.findMany({
        where: {
          status: 'PUBLISHED',
          OR: [
            { title: { contains: q, mode: 'insensitive' } },
            { description: { contains: q, mode: 'insensitive' } },
          ],
        },
        orderBy: [{ startsAt: 'asc' }, { id: 'asc' }],
        take: limit,
      });
      empty.events = this.visibility
        .filterEvents(ctx, events)
        .map((e) => ({
          id: e.id,
          title: e.title,
          startsAt: e.startsAt,
          status: e.status,
          visibility: e.visibility,
        }));
    }
    if (want(SearchType.POSTS)) {
      const posts = await this.prisma.post.findMany({
        where: {
          deletedAt: null,
          content: { contains: q, mode: 'insensitive' },
        },
        include: {
          author: { select: { id: true, displayName: true, avatarUrl: true } },
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
        },
        orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
        take: limit,
      });
      empty.posts = this.visibility
        .filterPosts(
          ctx,
          posts.map((p) => ({
            ...p,
            visibility: p.visibility as
              | 'PUBLIC'
              | 'UNIVERSITY'
              | 'COMMUNITY'
              | 'FOLLOWERS',
          })),
        )
        .map((p) => ({ id: p.id, content: p.content, createdAt: p.createdAt }));
    }
    return empty;
  }
}

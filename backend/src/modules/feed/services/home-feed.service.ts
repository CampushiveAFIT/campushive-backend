import { Injectable } from '@nestjs/common';
import { PrismaService } from '../../../config/prisma.service';
import { loadViewerContext } from '../../../common/access/viewer-context';
import { FeedCandidateService } from './feed-candidate.service';
import { FeedResponseService } from './feed-response.service';
import { FeedPage } from '../types/feed.types';

/**
 * Home Feed: followed users + self, own universities, own communities,
 * visible university/community events, plus a bounded public slice.
 */
@Injectable()
export class HomeFeedService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly candidates: FeedCandidateService,
    private readonly response: FeedResponseService,
  ) {}

  async get(
    viewerId: string,
    limit: number,
    cursor?: string,
  ): Promise<FeedPage> {
    const ctx = await loadViewerContext(this.prisma, viewerId);
    const authorIds = [...new Set([viewerId, ...ctx.followingIds])];
    const [byAuthors, byUnis, byComms, pubPosts, uniEvents, commEvents] =
      await Promise.all([
        this.candidates.postsByAuthors(authorIds),
        this.candidates.postsForUniversities(ctx.universityIds),
        this.candidates.postsForCommunities(ctx.communityIds),
        this.candidates.publicPosts(),
        this.candidates.eventsForUniversities(ctx.universityIds),
        this.candidates.eventsForCommunities(ctx.communityIds),
      ]);
    const posts = dedupePosts([byAuthors, byUnis, byComms, pubPosts]);
    const events = dedupeEvents([uniEvents, commEvents]);
    return this.response.assemble(ctx, posts, events, limit, cursor);
  }
}

export function dedupePosts<T extends { id: string }>(lists: T[][]): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const list of lists) {
    for (const item of list) {
      if (!seen.has(item.id)) {
        seen.add(item.id);
        out.push(item);
      }
    }
  }
  return out;
}

export function dedupeEvents<T extends { id: string }>(lists: T[][]): T[] {
  return dedupePosts(lists);
}

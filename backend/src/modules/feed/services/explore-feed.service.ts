import { Injectable } from '@nestjs/common';
import { PrismaService } from '../../../config/prisma.service';
import { loadViewerContext } from '../../../common/access/viewer-context';
import { FeedCandidateService } from './feed-candidate.service';
import { FeedResponseService } from './feed-response.service';
import { dedupeEvents, dedupePosts } from './home-feed.service';
import { FeedPage } from '../types/feed.types';

/**
 * Explore Feed: cross-university discovery. Only PUBLIC posts and PUBLIC
 * published events are candidates; visibility filtering still applies
 * (blocks, deleted content). Authentication is required.
 */
@Injectable()
export class ExploreFeedService {
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
    const [posts, events] = await Promise.all([
      this.candidates.publicPosts(),
      this.candidates.publicEvents(),
    ]);
    return this.response.assemble(
      ctx,
      dedupePosts([posts]),
      dedupeEvents([events]),
      limit,
      cursor,
    );
  }
}

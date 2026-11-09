import {
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { PrismaService } from '../../../config/prisma.service';
import { ApiErrorCode } from '../../../common/types/api-response';
import { loadViewerContext } from '../../../common/access/viewer-context';
import { FeedCandidateService } from './feed-candidate.service';
import { FeedResponseService } from './feed-response.service';
import { dedupeEvents, dedupePosts } from './home-feed.service';
import { FeedPage } from '../types/feed.types';

/** Community Feed: posts + published events of one community. */
@Injectable()
export class CommunityFeedService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly candidates: FeedCandidateService,
    private readonly response: FeedResponseService,
  ) {}

  async get(
    viewerId: string,
    communityId: string,
    limit: number,
    cursor?: string,
  ): Promise<FeedPage> {
    const community = await this.prisma.community.findUnique({
      where: { id: communityId },
    });
    if (!community || !community.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Community not found.',
      });
    }
    const ctx = await loadViewerContext(this.prisma, viewerId);
    if (
      community.visibility === 'PRIVATE' &&
      !ctx.communityIds.includes(community.id)
    ) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'This community is private.',
      });
    }
    const [posts, events] = await Promise.all([
      this.candidates.postsForCommunities([community.id]),
      this.candidates.eventsForCommunities([community.id]),
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

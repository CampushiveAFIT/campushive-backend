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

/** University Feed: posts + published events of one university (members only). */
@Injectable()
export class UniversityFeedService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly candidates: FeedCandidateService,
    private readonly response: FeedResponseService,
  ) {}

  async get(
    viewerId: string,
    universityId: string | undefined,
    limit: number,
    cursor?: string,
  ): Promise<FeedPage> {
    const ctx = await loadViewerContext(this.prisma, viewerId);
    const target =
      universityId ??
      ctx.universityIds[0] ??
      (() => {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'Join a university to view its feed.',
        });
      })();
    const university = await this.prisma.university.findUnique({
      where: { id: target },
    });
    if (!university || !university.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'University not found.',
      });
    }
    if (!ctx.universityIds.includes(university.id)) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'University feeds are restricted to its members.',
      });
    }
    const [posts, events] = await Promise.all([
      this.candidates.postsForUniversities([university.id]),
      this.candidates.eventsForUniversities([university.id]),
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

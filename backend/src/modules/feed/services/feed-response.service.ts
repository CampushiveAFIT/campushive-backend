import { Injectable } from '@nestjs/common';
import { ViewerContext } from '../../../common/access/viewer-context';
import { PostsService, PostRow } from '../../posts/posts.service';
import { EventsService, EventRow } from '../../events/events.service';
import { FeedVisibilityService } from './feed-visibility.service';
import { FeedRankingService, RankedItem } from './feed-ranking.service';
import { FeedItem, FeedPage } from '../types/feed.types';

type RankedWithRows = RankedItem & { postRow?: PostRow; eventRow?: EventRow };
@Injectable()
export class FeedResponseService {
  constructor(
    private readonly posts: PostsService,
    private readonly events: EventsService,
    private readonly visibility: FeedVisibilityService,
    private readonly ranking: FeedRankingService,
  ) {}

  async assemble(
    ctx: ViewerContext,
    postRows: PostRow[],
    eventRows: EventRow[],
    limit: number,
    rawCursor?: string,
  ): Promise<FeedPage> {
    const visiblePosts = this.visibility.filterPosts(ctx, postRows);
    const visibleEvents = this.visibility.filterEvents(ctx, eventRows);

    const stubs: RankedWithRows[] = [
      ...visiblePosts.map((p) => ({
        type: 'POST' as const,
        id: p.id,
        createdAt: p.createdAt,
        score: this.ranking.scorePost(p.createdAt, p.likeCount, p.commentCount),
        postRow: p,
      })),
      ...visibleEvents.map((e) => ({
        type: 'EVENT' as const,
        id: e.id,
        createdAt: e.createdAt,
        score: this.ranking.scoreEvent(e.createdAt, e.registeredCount),
        eventRow: e,
      })),
    ];
    const ranked = this.ranking.rank(stubs);
    const cursor =
      rawCursor !== undefined && rawCursor !== null && rawCursor !== ''
        ? this.ranking.decodeCursor(rawCursor)
        : null;
    const page = this.ranking.paginate(ranked, limit, cursor);

    const postViews = await this.posts.presentPosts(
      ctx.userId,
      ctx,
      page.items
        .filter((i) => i.type === 'POST' && i.postRow)
        .map((i) => i.postRow as PostRow),
    );
    const postById = new Map(postViews.map((p) => [p.id, p]));
    const items: FeedItem[] = page.items.map((i) => {
      if (i.type === 'POST') {
        const post = postById.get(i.id);
        if (!post) {
          throw new Error(`presenter dropped post ${i.id}`);
        }
        return { type: 'POST' as const, id: i.id, createdAt: i.createdAt, score: i.score, post };
      }
      const row = (i as RankedWithRows).eventRow;
      if (!row) {
        throw new Error(`missing event row for ${i.id}`);
      }
      return {
        type: 'EVENT' as const,
        id: i.id,
        createdAt: i.createdAt,
        score: i.score,
        event: this.events.presentEvent(row),
      };
    });
    return { items, nextCursor: page.nextCursor };
  }
}

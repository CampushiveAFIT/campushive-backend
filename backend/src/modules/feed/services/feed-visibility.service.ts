import { Injectable } from '@nestjs/common';
import {
  ViewerContext,
  canViewPost,
  PostAccessShape,
} from '../../../common/access/viewer-context';
import { eventSummaryVisible } from '../../../common/access/event-visibility';

export interface EventAccessShape {
  id: string;
  organizerId: string;
  universityId: string;
  communityId: string | null;
  visibility: 'PUBLIC' | 'UNIVERSITY' | 'COMMUNITY';
  status: string;
}

/**
 * Visibility / Safety Filtering: the single choke point between retrieval
 * and ranking. Reuses the Slice 2/3 visibility primitives so feeds can never
 * expose what detail endpoints would hide. Pure functions over candidates.
 */
@Injectable()
export class FeedVisibilityService {
  filterPosts<T extends PostAccessShape>(ctx: ViewerContext, posts: T[]): T[] {
    return posts.filter((p) => canViewPost(ctx, p));
  }

  filterEvents<T extends EventAccessShape>(ctx: ViewerContext, events: T[]): T[] {
    return events.filter((e) => {
      if (e.organizerId === ctx.userId) return true;
      if (e.status !== 'PUBLISHED') return false;
      if (ctx.blockedIds.includes(e.organizerId)) return false;
      return eventSummaryVisible(ctx, {
        id: e.id,
        organizerId: e.organizerId,
        universityId: e.universityId,
        communityId: e.communityId,
        title: '',
        startsAt: new Date(0),
        endsAt: null,
        location: null,
        status: e.status,
        visibility: e.visibility,
      });
    });
  }
}

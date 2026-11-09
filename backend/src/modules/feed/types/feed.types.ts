import type { EventView } from '../../events/events.service';
import type { PostView } from '../../posts/posts.service';

export type FeedItemType = 'POST' | 'EVENT';

/**
 * A feed item is a projection of an existing Post or Event — never a new
 * database entity. No feed table exists; ranking/pagination happen over
 * in-memory projections of a bounded candidate window (see ranking service).
 */
export interface FeedItem {
  type: FeedItemType;
  id: string;
  createdAt: Date;
  /** Deterministic score, see feed-ranking.service.ts. Higher ranks first. */
  score: number;
  post?: PostView;
  event?: EventView;
}

export interface FeedPage {
  items: FeedItem[];
  nextCursor: string | null;
}

/** Candidate window cap per retrieval source (documented bound). */
export const FEED_CANDIDATE_LIMIT = 200;

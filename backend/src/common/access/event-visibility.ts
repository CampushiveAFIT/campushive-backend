import { ViewerContext } from './viewer-context';

export interface EventSummaryShape {
  id: string;
  organizerId: string;
  universityId: string;
  communityId: string | null;
  title: string;
  startsAt: Date;
  endsAt: Date | null;
  location: string | null;
  status: string;
  visibility: 'PUBLIC' | 'UNIVERSITY' | 'COMMUNITY';
}

/**
 * Whether a referenced event's summary may be embedded in a post response.
 * Pure function (no I/O): organizers always see their events; drafts are
 * organizer-only; otherwise the event's own visibility rules plus block
 * rules apply. Cancelled-event ticket holders use the event detail endpoint
 * instead — summaries stay simple and list-safe.
 */
export function eventSummaryVisible(
  ctx: ViewerContext,
  event: EventSummaryShape,
): boolean {
  if (event.organizerId === ctx.userId) return true;
  if (event.status === 'DRAFT') return false;
  if (ctx.blockedIds.includes(event.organizerId)) return false;
  switch (event.visibility) {
    case 'PUBLIC':
      return true;
    case 'UNIVERSITY':
      return ctx.universityIds.includes(event.universityId);
    case 'COMMUNITY':
      return (
        event.communityId !== null &&
        ctx.communityIds.includes(event.communityId)
      );
    default:
      return false;
  }
}

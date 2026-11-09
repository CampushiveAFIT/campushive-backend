import { PrismaService } from '../../config/prisma.service';

/**
 * Per-request viewer context, loaded once and reused for every
 * authorization decision in Slice 2 list/detail endpoints.
 */
export interface ViewerContext {
  userId: string;
  /** Universities with an ACTIVE membership. */
  universityIds: string[];
  /** Communities with an ACTIVE membership. */
  communityIds: string[];
  /** User IDs the viewer follows. */
  followingIds: string[];
  /** User IDs blocked in EITHER direction with the viewer. */
  blockedIds: string[];
}

export interface PostAccessShape {
  authorId: string;
  universityId: string;
  communityId: string | null;
  visibility: 'PUBLIC' | 'UNIVERSITY' | 'COMMUNITY' | 'FOLLOWERS';
  deletedAt: Date | null;
}

export async function loadViewerContext(
  prisma: PrismaService,
  userId: string,
): Promise<ViewerContext> {
  const [uni, comm, following, given, received] = await Promise.all([
    prisma.universityMembership.findMany({
      where: { userId, status: 'ACTIVE' },
      select: { universityId: true },
    }),
    prisma.communityMembership.findMany({
      where: { userId, status: 'ACTIVE' },
      select: { communityId: true },
    }),
    prisma.follow.findMany({
      where: { followerId: userId },
      select: { followingId: true },
    }),
    prisma.block.findMany({
      where: { blockerId: userId },
      select: { blockedId: true },
    }),
    prisma.block.findMany({
      where: { blockedId: userId },
      select: { blockerId: true },
    }),
  ]);
  const blocked = new Set<string>();
  for (const b of given) blocked.add(b.blockedId);
  for (const b of received) blocked.add(b.blockerId);
  return {
    userId,
    universityIds: uni.map((m) => m.universityId),
    communityIds: comm.map((m) => m.communityId),
    followingIds: following.map((f) => f.followingId),
    blockedIds: [...blocked],
  };
}

/**
 * Single source of truth for post visibility.
 * Deleted posts are invisible to everyone (including the author).
 * A block in either direction hides content both ways.
 */
export function canViewPost(ctx: ViewerContext, post: PostAccessShape): boolean {
  if (post.deletedAt) return false;
  if (post.authorId === ctx.userId) return true;
  if (ctx.blockedIds.includes(post.authorId)) return false;
  switch (post.visibility) {
    case 'PUBLIC':
      return true;
    case 'UNIVERSITY':
      return ctx.universityIds.includes(post.universityId);
    case 'COMMUNITY':
      return (
        post.communityId !== null && ctx.communityIds.includes(post.communityId)
      );
    case 'FOLLOWERS':
      return ctx.followingIds.includes(post.authorId);
    default:
      return false;
  }
}

/** True when either direction block exists between two users. */
export function isBlockedEitherWay(ctx: ViewerContext, otherUserId: string): boolean {
  return otherUserId !== ctx.userId && ctx.blockedIds.includes(otherUserId);
}

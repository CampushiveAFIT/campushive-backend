import {
  BadRequestException,
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  ParsedPage,
  olderThan,
  toPage,
  PageResult,
} from '../../common/pagination/cursor';
import {
  ViewerContext,
  canViewPost,
  loadViewerContext,
} from '../../common/access/viewer-context';
import {
  EventSummaryShape,
  eventSummaryVisible,
} from '../../common/access/event-visibility';
import { CreatePostDto, UpdatePostDto } from './dto/post.dto';

export interface PostAuthor {
  id: string;
  displayName: string | null;
  avatarUrl: string | null;
}

export interface EventSummaryView {
  id: string;
  title: string;
  startsAt: Date;
  endsAt: Date | null;
  location: string | null;
  status: string;
  visibility: string;
}

export interface PostView {
  id: string;
  author: PostAuthor;
  universityId: string;
  communityId: string | null;
  content: string;
  visibility: string;
  likeCount: number;
  commentCount: number;
  likedByMe: boolean;
  event: EventSummaryView | null;
  createdAt: Date;
  updatedAt: Date;
}

type PostEventRow = EventSummaryShape & {
  organizerId: string;
  universityId: string;
};

export type PostRow = {
  id: string;
  authorId: string;
  universityId: string;
  communityId: string | null;
  content: string;
  visibility: 'PUBLIC' | 'UNIVERSITY' | 'COMMUNITY' | 'FOLLOWERS';
  likeCount: number;
  commentCount: number;
  deletedAt: Date | null;
  createdAt: Date;
  updatedAt: Date;
  author: PostAuthor;
  event: PostEventRow | null;
};

@Injectable()
export class PostsService {
  constructor(private readonly prisma: PrismaService) {}

  // -------------------------------------------------------------- helpers
  private async activeUserOrThrow(id: string) {
    const u = await this.prisma.user.findUnique({ where: { id } });
    if (!u || !u.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'User not found.',
      });
    }
    return u;
  }

  private async myActiveMembership(userId: string) {
    return this.prisma.universityMembership.findFirst({
      where: { userId, status: 'ACTIVE' },
      include: { university: true },
    });
  }

  private toView(
    row: PostRow,
    likedByMe: boolean,
    ctx: ViewerContext,
  ): PostView {
    return {
      id: row.id,
      author: row.author,
      universityId: row.universityId,
      communityId: row.communityId,
      content: row.content,
      visibility: row.visibility,
      likeCount: row.likeCount,
      commentCount: row.commentCount,
      likedByMe,
      event:
        row.event && eventSummaryVisible(ctx, row.event)
          ? {
              id: row.event.id,
              title: row.event.title,
              startsAt: row.event.startsAt,
              endsAt: row.event.endsAt,
              location: row.event.location,
              status: row.event.status,
              visibility: row.event.visibility,
            }
          : null,
      createdAt: row.createdAt,
      updatedAt: row.updatedAt,
    };
  }

  private async withLikedByMe(
    viewerId: string,
    ctx: ViewerContext,
    rows: PostRow[],
  ): Promise<PostView[]> {
    if (rows.length === 0) return [];
    const likes = await this.prisma.like.findMany({
      where: { userId: viewerId, postId: { in: rows.map((r) => r.id) } },
      select: { postId: true },
    });
    const liked = new Set(likes.map((l) => l.postId));
    return rows.map((r) => this.toView(r, liked.has(r.id), ctx));
  }

  private visibilityOr(ctx: ViewerContext): Prisma.PostWhereInput[] {
    const branches: Prisma.PostWhereInput[] = [{ visibility: 'PUBLIC' }];
    if (ctx.universityIds.length > 0) {
      branches.push({
        visibility: 'UNIVERSITY',
        universityId: { in: ctx.universityIds },
      });
    }
    if (ctx.communityIds.length > 0) {
      branches.push({
        visibility: 'COMMUNITY',
        communityId: { in: ctx.communityIds },
      });
    }
    if (ctx.followingIds.length > 0) {
      branches.push({
        visibility: 'FOLLOWERS',
        authorId: { in: ctx.followingIds },
      });
    }
    return branches;
  }

  private baseListWhere(ctx: ViewerContext): Prisma.PostWhereInput {
    const blocked =
      ctx.blockedIds.length > 0
        ? { authorId: { notIn: ctx.blockedIds } }
        : {};
    return { deletedAt: null, ...blocked, OR: this.visibilityOr(ctx) };
  }

  private async pagedPosts(
    ctx: ViewerContext,
    where: Prisma.PostWhereInput,
    page: ParsedPage,
  ): Promise<PageResult<PostView>> {
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = (await this.prisma.post.findMany({
      where: cursorWhere,
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
      take: page.limit + 1,
    })) as PostRow[];
    const result = toPage(rows, page.limit);
    return {
      items: await this.withLikedByMe(ctx.userId, ctx, result.items),
      nextCursor: result.nextCursor,
    };
  }

  /** Fetch a post for interaction gates: 404 when missing, deleted, or invisible. */
  async gate(viewerId: string, postId: string) {
    const post = await this.prisma.post.findUnique({
      where: { id: postId },
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
    });
    if (!post || post.deletedAt) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Post not found.',
      });
    }
    const ctx = await loadViewerContext(this.prisma, viewerId);
    const shape = {
      authorId: post.authorId,
      universityId: post.universityId,
      communityId: post.communityId,
      visibility: post.visibility,
      deletedAt: post.deletedAt,
    };
    if (!canViewPost(ctx, shape)) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Post not found.',
      });
    }
    return { post: post as PostRow, ctx };
  }

  // ---------------------------------------------------------------- create
  async create(authorId: string, dto: CreatePostDto): Promise<PostView> {
    const membership = await this.myActiveMembership(authorId);
    if (!membership || !membership.university.isActive) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Join an active university before posting.',
      });
    }
    let communityId: string | null = null;
    if (dto.communityId) {
      const community = await this.prisma.community.findUnique({
        where: { id: dto.communityId },
      });
      if (!community || !community.isActive) {
        throw new NotFoundException({
          code: ApiErrorCode.NOT_FOUND,
          message: 'Community not found.',
        });
      }
      if (community.universityId !== membership.universityId) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'Communities must belong to your own university.',
        });
      }
      const member = await this.prisma.communityMembership.findUnique({
        where: {
          userId_communityId: { userId: authorId, communityId: community.id },
        },
      });
      if (!member || member.status !== 'ACTIVE') {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'Join the community before posting into it.',
        });
      }
      communityId = community.id;
    }
    const visibility = dto.visibility ?? 'UNIVERSITY';
    if (visibility === 'COMMUNITY' && !communityId) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Community posts require a community.',
      });
    }
    const eventId = await this.resolveEvent(
      authorId,
      membership.universityId,
      dto.eventId,
    );
    const created = (await this.prisma.post.create({
      data: {
        authorId,
        universityId: membership.universityId,
        communityId,
        content: dto.content,
        visibility,
        eventId,
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
    })) as PostRow;
    const ctx = await loadViewerContext(this.prisma, authorId);
    return this.toView(created, false, ctx);
  }

  /**
   * Validate an event reference: the event must exist, be published, belong
   * to the author's university, and satisfy community/block rules. Only the
   * event ID is stored — no event data is ever copied into the post.
   */
  private async resolveEvent(
    authorId: string,
    universityId: string,
    eventId: string | undefined,
  ): Promise<string | null> {
    if (!eventId) return null;
    const event = await this.prisma.event.findUnique({
      where: { id: eventId },
    });
    if (!event) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Event not found.',
      });
    }
    if (event.status !== 'PUBLISHED') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Only published events can be referenced.',
      });
    }
    if (event.universityId !== universityId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Events must belong to your own university.',
      });
    }
    if (event.communityId) {
      const member = await this.prisma.communityMembership.findUnique({
        where: {
          userId_communityId: { userId: authorId, communityId: event.communityId },
        },
      });
      if (!member || member.status !== 'ACTIVE') {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'Community events require community membership.',
        });
      }
    }
    const blocked = await this.prisma.block.findFirst({
      where: {
        OR: [
          { blockerId: authorId, blockedId: event.organizerId },
          { blockerId: event.organizerId, blockedId: authorId },
        ],
      },
      select: { id: true },
    });
    if (blocked) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'You cannot reference this event.',
      });
    }
    return event.id;
  }

  // ------------------------------------------------------------------ detail
  async detail(viewerId: string, id: string): Promise<PostView> {
    const { post, ctx } = await this.gate(viewerId, id);
    const liked =
      (await this.prisma.like.count({
        where: { userId: viewerId, postId: id },
      })) > 0;
    return this.toView(post, liked, ctx);
  }

  // ------------------------------------------------------------------ update
  async update(
    viewerId: string,
    id: string,
    dto: UpdatePostDto,
  ): Promise<PostView> {
    const { post } = await this.gate(viewerId, id);
    if (post.authorId !== viewerId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the author can update this post.',
      });
    }
    const data: { content?: string; visibility?: typeof post.visibility } = {};
    if (dto.content !== undefined) data.content = dto.content;
    if (dto.visibility !== undefined) {
      if (dto.visibility === 'COMMUNITY' && !post.communityId) {
        throw new BadRequestException({
          code: ApiErrorCode.VALIDATION_ERROR,
          message: 'Community posts require a community.',
        });
      }
      data.visibility = dto.visibility;
    }
    if (Object.keys(data).length === 0) {
      const ctx0 = await loadViewerContext(this.prisma, viewerId);
      return this.toView(post, await this.likedBy(viewerId, id), ctx0);
    }
    const updated = (await this.prisma.post.update({
      where: { id },
      data,
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
    })) as PostRow;
    const ctx1 = await loadViewerContext(this.prisma, viewerId);
    return this.toView(updated, await this.likedBy(viewerId, id), ctx1);
  }

  // ------------------------------------------------------------------ remove
  async remove(viewerId: string, id: string): Promise<{ deleted: boolean }> {
    const { post } = await this.gate(viewerId, id);
    if (post.authorId !== viewerId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the author can delete this post.',
      });
    }
    await this.prisma.post.update({
      where: { id },
      data: { deletedAt: new Date() },
    });
    return { deleted: true };
  }

  private async likedBy(viewerId: string, postId: string): Promise<boolean> {
    return (
      (await this.prisma.like.count({ where: { userId: viewerId, postId } })) > 0
    );
  }

  /**
   * Public projection used by the feed system: renders post rows (already
   * visibility-filtered by the caller) into PostViews with likedByMe and
   * gated event summaries. No authorization performed here.
   */
  async presentPosts(
    viewerId: string,
    ctx: ViewerContext,
    rows: PostRow[],
  ): Promise<PostView[]> {
    return this.withLikedByMe(viewerId, ctx, rows);
  }

  // ------------------------------------------------------------------- lists
  async listByAuthor(
    viewerId: string,
    authorId: string,
    page: ParsedPage,
  ): Promise<PageResult<PostView>> {
    await this.activeUserOrThrow(authorId);
    const ctx = await loadViewerContext(this.prisma, viewerId);
    if (viewerId !== authorId) {
      if (ctx.blockedIds.includes(authorId)) {
        return { items: [], nextCursor: null };
      }
      return this.pagedPosts(
        ctx,
        { authorId, ...this.baseListWhere(ctx) },
        page,
      );
    }
    const rows = (await this.prisma.post.findMany({
      where:
        page.cursor != null
          ? {
              AND: [
                { authorId, deletedAt: null },
                olderThan(page.cursor.createdAt, page.cursor.id),
              ],
            }
          : { authorId, deletedAt: null },
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
      take: page.limit + 1,
    })) as PostRow[];
    const result = toPage(rows, page.limit);
    return {
      items: await this.withLikedByMe(viewerId, ctx, result.items),
      nextCursor: result.nextCursor,
    };
  }

  async listByUniversity(
    viewerId: string,
    slug: string,
    page: ParsedPage,
  ): Promise<PageResult<PostView>> {
    const university = await this.prisma.university.findUnique({
      where: { slug },
    });
    if (!university || !university.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'University not found.',
      });
    }
    const ctx = await loadViewerContext(this.prisma, viewerId);
    if (!ctx.universityIds.includes(university.id)) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'University posts are restricted to its members.',
      });
    }
    return this.pagedPosts(
      ctx,
      { universityId: university.id, ...this.baseListWhere(ctx) },
      page,
    );
  }

  async listByCommunity(
    viewerId: string,
    communityId: string,
    page: ParsedPage,
  ): Promise<PageResult<PostView>> {
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
    return this.pagedPosts(
      ctx,
      { communityId: community.id, ...this.baseListWhere(ctx) },
      page,
    );
  }
}

import {
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  DomainEventTypes,
  buildOutboxEvent,
} from '../notifications/outbox/domain-event';
import {
  ParsedPage,
  encodeCursor,
  newerThan,
  PageResult,
} from '../../common/pagination/cursor';
import { PostsService } from './posts.service';
import { CreateCommentDto, UpdateCommentDto } from './dto/post.dto';

export interface CommentView {
  id: string;
  postId: string;
  author: { id: string; displayName: string | null; avatarUrl: string | null };
  content: string;
  createdAt: Date;
  updatedAt: Date;
}

@Injectable()
export class CommentsService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly posts: PostsService,
  ) {}

  private toView(c: {
    id: string;
    postId: string;
    content: string;
    createdAt: Date;
    updatedAt: Date;
    author: { id: string; displayName: string | null; avatarUrl: string | null };
  }): CommentView {
    return { ...c };
  }

  async create(
    viewerId: string,
    postId: string,
    dto: CreateCommentDto,
  ): Promise<CommentView> {
    // Gate: missing, deleted, invisible, or blocked → 404.
    await this.posts.gate(viewerId, postId);
    const created = await this.prisma.$transaction(async (tx) => {
      const comment = await tx.comment.create({
        data: { postId, authorId: viewerId, content: dto.content },
        include: {
          author: { select: { id: true, displayName: true, avatarUrl: true } },
        },
      });
      await tx.post.update({
        where: { id: postId },
        data: { commentCount: { increment: 1 } },
      });
      // Outbox: persisted atomically with the comment itself.
      await tx.domainEvent.create({
        data: buildOutboxEvent(
          DomainEventTypes.POST_COMMENTED,
          'Post',
          postId,
          viewerId,
          { postId, commentId: comment.id },
        ),
      });
      return comment;
    });
    return this.toView(created);
  }

  async list(
    viewerId: string,
    postId: string,
    page: ParsedPage,
  ): Promise<PageResult<CommentView>> {
    await this.posts.gate(viewerId, postId);
    const where = { postId, deletedAt: null };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, newerThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    // Conversation order: oldest first.
    const rows = await this.prisma.comment.findMany({
      where: cursorWhere,
      include: {
        author: { select: { id: true, displayName: true, avatarUrl: true } },
      },
      orderBy: [{ createdAt: 'asc' }, { id: 'asc' }],
      take: page.limit + 1,
    });
    const items = rows.slice(0, page.limit).map((c) => this.toView(c));
    const nextCursor =
      rows.length > page.limit
        ? encodeCursor(
            items[items.length - 1].createdAt,
            items[items.length - 1].id,
          )
        : null;
    return { items, nextCursor };
  }

  private async ownedOrThrow(viewerId: string, id: string) {
    const comment = await this.prisma.comment.findUnique({ where: { id } });
    // 404 for missing, deleted, or not-owned: prevents existence probing.
    if (!comment || comment.deletedAt || comment.authorId !== viewerId) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Comment not found.',
      });
    }
    return comment;
  }

  async update(
    viewerId: string,
    id: string,
    dto: UpdateCommentDto,
  ): Promise<CommentView> {
    await this.ownedOrThrow(viewerId, id);
    const updated = await this.prisma.comment.update({
      where: { id },
      data: { content: dto.content },
      include: {
        author: { select: { id: true, displayName: true, avatarUrl: true } },
      },
    });
    return this.toView(updated);
  }

  async remove(viewerId: string, id: string): Promise<{ deleted: boolean }> {
    const comment = await this.prisma.comment.findUnique({ where: { id } });
    if (!comment || comment.deletedAt) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Comment not found.',
      });
    }
    const post = await this.prisma.post.findUnique({
      where: { id: comment.postId },
      select: { authorId: true },
    });
    const isAuthor = comment.authorId === viewerId;
    const isPostOwner = post !== null && post.authorId === viewerId;
    if (!isAuthor && !isPostOwner) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the author or the post owner can delete this comment.',
      });
    }
    await this.prisma.$transaction([
      this.prisma.comment.update({
        where: { id },
        data: { deletedAt: new Date() },
      }),
      this.prisma.post.update({
        where: { id: comment.postId },
        data: { commentCount: { decrement: 1 } },
      }),
    ]);
    return { deleted: true };
  }
}

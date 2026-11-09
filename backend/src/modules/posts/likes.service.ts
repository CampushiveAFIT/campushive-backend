import { Injectable } from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import {
  DomainEventTypes,
  buildOutboxEvent,
} from '../notifications/outbox/domain-event';
import { PostsService } from './posts.service';

@Injectable()
export class LikesService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly posts: PostsService,
  ) {}

  async like(viewerId: string, postId: string) {
    // Gate first: missing, deleted, invisible, or blocked → 404.
    await this.posts.gate(viewerId, postId);
    const existing = await this.prisma.like.findUnique({
      where: { userId_postId: { userId: viewerId, postId } },
      include: { post: { select: { likeCount: true } } },
    });
    if (existing) {
      return { liked: true, likeCount: existing.post.likeCount };
    }
    try {
      const created = await this.prisma.$transaction(async (tx) => {
        const like = await tx.like.create({ data: { userId: viewerId, postId } });
        const updated = await tx.post.update({
          where: { id: postId },
          data: { likeCount: { increment: 1 } },
          select: { likeCount: true },
        });
        // Outbox: persisted atomically with the like itself.
        await tx.domainEvent.create({
          data: buildOutboxEvent(
            DomainEventTypes.POST_LIKED,
            'Post',
            postId,
            viewerId,
            { postId, likeId: like.id },
          ),
        });
        return updated;
      });
      return { liked: true, likeCount: created.likeCount };
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        // Concurrent double-like: return the winner's state idempotently.
        const current = await this.prisma.post.findUniqueOrThrow({
          where: { id: postId },
          select: { likeCount: true },
        });
        return { liked: true, likeCount: current.likeCount };
      }
      throw e;
    }
  }

  async unlike(viewerId: string, postId: string) {
    await this.posts.gate(viewerId, postId);
    const existing = await this.prisma.like.findUnique({
      where: { userId_postId: { userId: viewerId, postId } },
    });
    if (!existing) {
      const current = await this.prisma.post.findUniqueOrThrow({
        where: { id: postId },
        select: { likeCount: true },
      });
      return { liked: false, likeCount: current.likeCount };
    }
    const updated = await this.prisma.$transaction(async (tx) => {
      await tx.like.delete({ where: { id: existing.id } });
      return tx.post.update({
        where: { id: postId },
        data: { likeCount: { decrement: 1 } },
        select: { likeCount: true },
      });
    });
    return {
      liked: false,
      likeCount: Math.max(0, updated.likeCount),
    };
  }
}

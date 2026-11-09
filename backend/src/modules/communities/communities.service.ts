import {
  BadRequestException,
  ConflictException,
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import { UniversitiesService } from '../universities/universities.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  ParsedPage,
  olderThan,
  toPage,
  PageResult,
} from '../../common/pagination/cursor';
import {
  CreateCommunityDto,
  UpdateCommunityDto,
} from './dto/community.dto';

export interface CommunityView {
  id: string;
  universityId: string;
  name: string;
  slug: string;
  description: string | null;
  type: string;
  visibility: string;
  ownerId: string | null;
  isActive: boolean;
  memberCount: number;
  createdAt: Date;
  updatedAt: Date;
}

@Injectable()
export class CommunitiesService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly universities: UniversitiesService,
  ) {}

  // ------------------------------------------------------------- helpers
  private async activeCommunityOrThrow(id: string) {
    const c = await this.prisma.community.findUnique({ where: { id } });
    if (!c || !c.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Community not found.',
      });
    }
    return c;
  }

  private async myMembership(userId: string, communityId: string) {
    return this.prisma.communityMembership.findUnique({
      where: { userId_communityId: { userId, communityId } },
    });
  }

  private async myActiveUniversityIds(userId: string): Promise<string[]> {
    const rows = await this.prisma.universityMembership.findMany({
      where: { userId, status: 'ACTIVE' },
      select: { universityId: true },
    });
    return rows.map((r) => r.universityId);
  }

  /** Throws 403 unless the viewer may see the community. */
  private async requireCanView(
    userId: string,
    community: { id: string; universityId: string; visibility: string },
  ) {
    if (community.visibility === 'PUBLIC') return;
    if (community.visibility === 'UNIVERSITY') {
      const mine = await this.myActiveUniversityIds(userId);
      if (!mine.includes(community.universityId)) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'This community is restricted to university members.',
        });
      }
      return;
    }
    // PRIVATE: active members only.
    const m = await this.myMembership(userId, community.id);
    if (!m || m.status !== 'ACTIVE') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'This community is private.',
      });
    }
  }

  private async toView(c: {
    id: string;
    universityId: string;
    name: string;
    slug: string;
    description: string | null;
    type: string;
    visibility: string;
    ownerId: string | null;
    isActive: boolean;
    createdAt: Date;
    updatedAt: Date;
  }): Promise<CommunityView> {
    const memberCount = await this.prisma.communityMembership.count({
      where: { communityId: c.id, status: 'ACTIVE' },
    });
    return { ...c, memberCount };
  }

  // ---------------------------------------------------------------- create
  async create(userId: string, dto: CreateCommunityDto): Promise<CommunityView> {
    // The community must belong to the creator's own ACTIVE university:
    // universityId is validated, never trusted blindly.
    let mine: { university: { id: string } };
    try {
      mine = await this.universities.myUniversity(userId);
    } catch {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Join a university before creating a community.',
      });
    }
    if (mine.university.id !== dto.universityId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Communities can only be created in your own university.',
      });
    }
    try {
      const created = await this.prisma.$transaction(async (tx) => {
        const community = await tx.community.create({
          data: {
            universityId: dto.universityId,
            name: dto.name.trim(),
            slug: dto.slug,
            description: dto.description?.trim() ? dto.description.trim() : null,
            type: dto.type ?? 'CLUB',
            visibility: dto.visibility ?? 'UNIVERSITY',
            ownerId: userId,
          },
        });
        await tx.communityMembership.create({
          data: {
            userId,
            communityId: community.id,
            role: 'OWNER',
            status: 'ACTIVE',
          },
        });
        return community;
      });
      return this.toView(created);
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        throw new ConflictException({
          code: ApiErrorCode.CONFLICT,
          message: 'A community with this slug already exists in the university.',
        });
      }
      throw e;
    }
  }

  // ------------------------------------------------------------------- read
  async detail(userId: string, id: string) {
    const c = await this.activeCommunityOrThrow(id);
    await this.requireCanView(userId, c);
    const mine = await this.myMembership(userId, id);
    return {
      community: await this.toView(c),
      myMembership: mine
        ? { role: mine.role, status: mine.status, createdAt: mine.createdAt }
        : null,
    };
  }

  async listByUniversity(
    slug: string,
    userId: string,
    page: ParsedPage,
  ): Promise<PageResult<CommunityView>> {
    const university = await this.universities.getBySlug(slug);
    const mine = await this.myActiveUniversityIds(userId);
    const isMember = mine.includes(university.id);
    const where = {
      universityId: university.id,
      isActive: true,
      OR: [
        { visibility: 'PUBLIC' as const },
        ...(isMember
          ? [
              { visibility: 'UNIVERSITY' as const },
              {
                memberships: {
                  some: { userId, status: 'ACTIVE' as const },
                },
              },
            ]
          : []),
      ],
    };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.community.findMany({
      where: cursorWhere,
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(rows, page.limit);
    return {
      items: await Promise.all(result.items.map((c) => this.toView(c))),
      nextCursor: result.nextCursor,
    };
  }

  // ------------------------------------------------------------------ update
  async update(
    userId: string,
    id: string,
    dto: UpdateCommunityDto,
  ): Promise<CommunityView> {
    const c = await this.activeCommunityOrThrow(id);
    const mine = await this.myMembership(userId, id);
    if (!mine || mine.status !== 'ACTIVE' || mine.role !== 'OWNER') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the community owner can update it.',
      });
    }
    const data: Record<string, unknown> = {};
    if (dto.name !== undefined) data.name = dto.name.trim();
    if (dto.slug !== undefined) data.slug = dto.slug;
    if (dto.description !== undefined) {
      data.description = dto.description.trim() ? dto.description.trim() : null;
    }
    if (dto.type !== undefined) data.type = dto.type;
    if (dto.visibility !== undefined) data.visibility = dto.visibility;
    if (Object.keys(data).length === 0) return this.toView(c);
    try {
      const updated = await this.prisma.community.update({
        where: { id },
        data: data as Prisma.CommunityUpdateInput,
      });
      return this.toView(updated);
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        throw new ConflictException({
          code: ApiErrorCode.CONFLICT,
          message: 'A community with this slug already exists in the university.',
        });
      }
      throw e;
    }
  }

  async deactivate(userId: string, id: string): Promise<CommunityView> {
    await this.activeCommunityOrThrow(id);
    const mine = await this.myMembership(userId, id);
    if (!mine || mine.status !== 'ACTIVE' || mine.role !== 'OWNER') {
      // Already-inactive handled above (404); non-owners are forbidden.
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the community owner can deactivate it.',
      });
    }
    const updated = await this.prisma.community.update({
      where: { id },
      data: { isActive: false },
    });
    return this.toView(updated);
  }

  // -------------------------------------------------------------- membership
  async join(userId: string, id: string) {
    const c = await this.activeCommunityOrThrow(id);
    if (c.visibility === 'PRIVATE') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'This community is invite-only.',
      });
    }
    if (c.visibility === 'UNIVERSITY') {
      const mine = await this.myActiveUniversityIds(userId);
      if (!mine.includes(c.universityId)) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'This community is restricted to university members.',
        });
      }
    }
    const existing = await this.myMembership(userId, id);
    if (existing) {
      if (existing.status === 'ACTIVE') {
        return this.memberView(existing);
      }
      // SUSPENDED/BANNED records persist: contact an owner for reinstatement.
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Your membership is not active. Contact a community owner.',
      });
    }
    try {
      const created = await this.prisma.communityMembership.create({
        data: { userId, communityId: id, role: 'MEMBER', status: 'ACTIVE' },
      });
      return this.memberView(created);
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        // Concurrent join: return the winner's record idempotently.
        const winner = await this.myMembership(userId, id);
        if (winner && winner.status === 'ACTIVE') return this.memberView(winner);
      }
      throw e;
    }
  }

  async leave(userId: string, id: string): Promise<{ left: boolean }> {
    await this.activeCommunityOrThrow(id);
    const mine = await this.myMembership(userId, id);
    if (!mine) return { left: false };
    if (mine.role === 'OWNER') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Owners cannot leave. Transfer ownership first.',
      });
    }
    // Voluntary leave removes the record; bans persist as BANNED rows.
    await this.prisma.communityMembership.delete({ where: { id: mine.id } });
    return { left: true };
  }

  async listMembers(userId: string, id: string, page: ParsedPage) {
    const c = await this.activeCommunityOrThrow(id);
    await this.requireCanView(userId, c);
    const where = { communityId: id };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.communityMembership.findMany({
      where: cursorWhere,
      include: {
        user: { select: { id: true, displayName: true, avatarUrl: true } },
      },
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(rows, page.limit);
    return {
      items: result.items.map((m) => ({
        user: m.user,
        role: m.role,
        status: m.status,
        createdAt: m.createdAt,
      })),
      nextCursor: result.nextCursor,
    };
  }

  async updateMemberRole(
    actorId: string,
    id: string,
    targetUserId: string,
    role: 'MEMBER' | 'MODERATOR',
  ) {
    await this.activeCommunityOrThrow(id);
    const actor = await this.myMembership(actorId, id);
    if (!actor || actor.status !== 'ACTIVE' || actor.role !== 'OWNER') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the community owner can manage roles.',
      });
    }
    const target = await this.prisma.communityMembership.findUnique({
      where: { userId_communityId: { userId: targetUserId, communityId: id } },
    });
    if (!target || target.status !== 'ACTIVE') {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Active membership not found.',
      });
    }
    if (target.role === 'OWNER') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'The owner role cannot be changed here.',
      });
    }
    const updated = await this.prisma.communityMembership.update({
      where: { id: target.id },
      data: { role },
      include: { user: { select: { id: true, displayName: true } } },
    });
    return this.memberView(updated);
  }

  async removeMember(actorId: string, id: string, targetUserId: string) {
    await this.activeCommunityOrThrow(id);
    if (actorId === targetUserId) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Use leave to exit a community.',
      });
    }
    const actor = await this.myMembership(actorId, id);
    if (!actor || actor.status !== 'ACTIVE') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only community managers can remove members.',
      });
    }
    const target = await this.prisma.communityMembership.findUnique({
      where: { userId_communityId: { userId: targetUserId, communityId: id } },
    });
    if (!target || target.status !== 'ACTIVE') {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Active membership not found.',
      });
    }
    if (target.role === 'OWNER') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'The owner cannot be removed.',
      });
    }
    if (actor.role === 'MODERATOR' && target.role !== 'MEMBER') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Moderators can only remove ordinary members.',
      });
    }
    if (actor.role !== 'OWNER' && actor.role !== 'MODERATOR') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only community managers can remove members.',
      });
    }
    // Removal persists as BANNED so rejoin stays blocked.
    const updated = await this.prisma.communityMembership.update({
      where: { id: target.id },
      data: { status: 'BANNED' },
    });
    return this.memberView(updated);
  }

  private memberView(m: {
    role: string;
    status: string;
    createdAt: Date;
    userId?: string;
    communityId?: string;
  }) {
    return { role: m.role, status: m.status, createdAt: m.createdAt };
  }
}

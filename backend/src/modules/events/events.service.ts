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
  DomainEventTypes,
  buildOutboxEvent,
} from '../notifications/outbox/domain-event';
import {
  ParsedPage,
  encodeCursor,
} from '../../common/pagination/cursor';
import {
  ViewerContext,
  loadViewerContext,
} from '../../common/access/viewer-context';
import { CreateEventDto, EventListQuery, UpdateEventDto } from './dto/event.dto';

export interface EventOrganizer {
  id: string;
  displayName: string | null;
  avatarUrl: string | null;
}

export interface EventView {
  id: string;
  organizer: EventOrganizer;
  universityId: string;
  communityId: string | null;
  title: string;
  description: string | null;
  type: string;
  location: string | null;
  startsAt: Date;
  endsAt: Date | null;
  capacity: number;
  registeredCount: number;
  availableSpots: number;
  registrationDeadline: Date | null;
  visibility: string;
  status: string;
  createdAt: Date;
  updatedAt: Date;
  myTicket?: { id: string; status: string } | null;
}

export type EventRow = {
  id: string;
  organizerId: string;
  universityId: string;
  communityId: string | null;
  title: string;
  description: string | null;
  type: string;
  location: string | null;
  startsAt: Date;
  endsAt: Date | null;
  capacity: number;
  registeredCount: number;
  registrationDeadline: Date | null;
  visibility: 'PUBLIC' | 'UNIVERSITY' | 'COMMUNITY';
  status: 'DRAFT' | 'PUBLISHED' | 'CANCELLED' | 'COMPLETED';
  createdAt: Date;
  updatedAt: Date;
  organizer: EventOrganizer;
};

const VALID_TRANSITIONS: Record<string, string[]> = {
  DRAFT: ['PUBLISHED', 'CANCELLED'],
  PUBLISHED: ['CANCELLED', 'COMPLETED'],
  CANCELLED: [],
  COMPLETED: [],
};

@Injectable()
export class EventsService {
  constructor(private readonly prisma: PrismaService) {}

  // -------------------------------------------------------------- helpers
  private toView(row: EventRow, myTicket?: { id: string; status: string } | null): EventView {
    return {
      id: row.id,
      organizer: row.organizer,
      universityId: row.universityId,
      communityId: row.communityId,
      title: row.title,
      description: row.description,
      type: row.type,
      location: row.location,
      startsAt: row.startsAt,
      endsAt: row.endsAt,
      capacity: row.capacity,
      registeredCount: row.registeredCount,
      availableSpots: Math.max(0, row.capacity - row.registeredCount),
      registrationDeadline: row.registrationDeadline,
      visibility: row.visibility,
      status: row.status,
      createdAt: row.createdAt,
      updatedAt: row.updatedAt,
      ...(myTicket !== undefined ? { myTicket } : {}),
    };
  }

  /**
   * Public projection used by the feed system: renders an event row
   * (already visibility-filtered by the caller) without ticket context.
   * No authorization performed here.
   */
  presentEvent(row: EventRow): EventView {
    return this.toView(row);
  }

  private async myActiveMembership(userId: string) {
    return this.prisma.universityMembership.findFirst({
      where: { userId, status: 'ACTIVE' },
      include: { university: true },
    });
  }

  /** Visibility gate for events (organizer sees everything). */
  async canViewEvent(
    ctx: ViewerContext,
    event: {
      organizerId: string;
      universityId: string;
      communityId: string | null;
      visibility: 'PUBLIC' | 'UNIVERSITY' | 'COMMUNITY';
      status: string;
      id: string;
    },
  ): Promise<boolean> {
    if (event.organizerId === ctx.userId) return true;
    if (event.status === 'DRAFT') return false;
    if (ctx.blockedIds.includes(event.organizerId)) return false;
    if (event.status === 'CANCELLED') {
      // Cancelled events are visible only to organizers and ticket holders.
      const mine = await this.prisma.ticket.findUnique({
        where: { eventId_userId: { eventId: event.id, userId: ctx.userId } },
        select: { id: true },
      });
      return mine !== null;
    }
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

  private async fetchOrThrow(id: string) {
    const event = await this.prisma.event.findUnique({
      where: { id },
      include: {
        organizer: { select: { id: true, displayName: true, avatarUrl: true } },
      },
    });
    if (!event) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Event not found.',
      });
    }
    return event as EventRow;
  }

  private assertDateCoherence(
    startsAt: Date,
    endsAt: Date | null | undefined,
    deadline: Date | null | undefined,
  ) {
    if (endsAt && endsAt.getTime() <= startsAt.getTime()) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'endsAt must be after startsAt.',
      });
    }
    if (deadline && deadline.getTime() > startsAt.getTime()) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'registrationDeadline cannot be after the event starts.',
      });
    }
  }

  private async resolveCommunity(
    organizerId: string,
    universityId: string,
    communityId: string | null | undefined,
  ): Promise<string | null> {
    if (communityId === undefined) return undefined as unknown as null;
    if (communityId === null) return null;
    const community = await this.prisma.community.findUnique({
      where: { id: communityId },
    });
    if (!community || !community.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Community not found.',
      });
    }
    if (community.universityId !== universityId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Community must belong to the event university.',
      });
    }
    const member = await this.prisma.communityMembership.findUnique({
      where: { userId_communityId: { userId: organizerId, communityId } },
    });
    if (!member || member.status !== 'ACTIVE') {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Join the community before attaching events to it.',
      });
    }
    return community.id;
  }

  // ---------------------------------------------------------------- create
  async create(organizerId: string, dto: CreateEventDto): Promise<EventView> {
    const membership = await this.myActiveMembership(organizerId);
    if (!membership || !membership.university.isActive) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Join an active university before creating events.',
      });
    }
    const universityId = membership.universityId;
    const communityId = await this.resolveCommunity(
      organizerId,
      universityId,
      dto.communityId,
    );
    const visibility = dto.visibility ?? 'UNIVERSITY';
    if (visibility === 'COMMUNITY' && !communityId) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Community events require a community.',
      });
    }
    const startsAt = new Date(dto.startsAt);
    const endsAt = dto.endsAt ? new Date(dto.endsAt) : null;
    const deadline = dto.registrationDeadline
      ? new Date(dto.registrationDeadline)
      : null;
    this.assertDateCoherence(startsAt, endsAt, deadline);
    const created = (await this.prisma.event.create({
      data: {
        organizerId,
        universityId,
        communityId,
        title: dto.title,
        description: dto.description?.trim() ? dto.description.trim() : null,
        type: dto.type ?? 'OTHER',
        location: dto.location?.trim() ? dto.location.trim() : null,
        startsAt,
        endsAt,
        capacity: dto.capacity,
        registrationDeadline: deadline,
        visibility,
        status: 'DRAFT',
      },
      include: {
        organizer: { select: { id: true, displayName: true, avatarUrl: true } },
      },
    })) as EventRow;
    return this.toView(created);
  }

  // ------------------------------------------------------------------ detail
  async detail(viewerId: string, id: string): Promise<EventView> {
    const event = await this.fetchOrThrow(id);
    const ctx = await loadViewerContext(this.prisma, viewerId);
    if (!(await this.canViewEvent(ctx, event))) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Event not found.',
      });
    }
    const mine = await this.prisma.ticket.findUnique({
      where: { eventId_userId: { eventId: id, userId: viewerId } },
      select: { id: true, status: true },
    });
    return this.toView(event, mine);
  }

  // ------------------------------------------------------------------ update
  async update(
    viewerId: string,
    id: string,
    dto: UpdateEventDto,
  ): Promise<EventView> {
    const event = await this.fetchOrThrow(id);
    if (event.organizerId !== viewerId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the organizer can update this event.',
      });
    }
    if (event.status === 'CANCELLED' || event.status === 'COMPLETED') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Closed events cannot be edited.',
      });
    }
    const data: Prisma.EventUpdateInput = {};
    if (dto.title !== undefined) data.title = dto.title;
    if (dto.description !== undefined) {
      data.description = dto.description.trim() ? dto.description.trim() : null;
    }
    if (dto.type !== undefined) data.type = dto.type;
    if (dto.location !== undefined) {
      data.location = dto.location.trim() ? dto.location.trim() : null;
    }
    const startsAt = dto.startsAt ? new Date(dto.startsAt) : event.startsAt;
    const endsAt = dto.endsAt ? new Date(dto.endsAt) : event.endsAt;
    // endsAt explicitly nulled? DTO has no null support for endsAt; keep value.
    const deadline =
      dto.registrationDeadline !== undefined
        ? dto.registrationDeadline
          ? new Date(dto.registrationDeadline)
          : null
        : event.registrationDeadline;
    if (dto.startsAt || dto.endsAt || dto.registrationDeadline !== undefined) {
      this.assertDateCoherence(startsAt, endsAt, deadline);
      if (dto.startsAt) data.startsAt = startsAt;
      if (dto.endsAt) data.endsAt = endsAt;
      if (dto.registrationDeadline !== undefined) data.registrationDeadline = deadline;
    }
    if (dto.capacity !== undefined) {
      if (dto.capacity < event.registeredCount) {
        throw new BadRequestException({
          code: ApiErrorCode.VALIDATION_ERROR,
          message: 'Capacity cannot drop below the registered count.',
        });
      }
      data.capacity = dto.capacity;
    }
    if (dto.communityId !== undefined) {
      const resolved = await this.resolveCommunity(
        viewerId,
        event.universityId,
        dto.communityId,
      );
      data.community =
        resolved === null ? { disconnect: true } : { connect: { id: resolved } };
    }
    const effectiveCommunity =
      dto.communityId !== undefined
        ? (dto.communityId as string | null)
        : event.communityId;
    if (dto.visibility !== undefined) {
      if (dto.visibility === 'COMMUNITY' && !effectiveCommunity) {
        throw new BadRequestException({
          code: ApiErrorCode.VALIDATION_ERROR,
          message: 'Community events require a community.',
        });
      }
      data.visibility = dto.visibility;
    } else if (
      event.visibility === 'COMMUNITY' &&
      dto.communityId === null
    ) {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Detaching the community requires changing visibility first.',
      });
    }
    if (dto.status !== undefined && dto.status !== event.status) {
      const allowed = VALID_TRANSITIONS[event.status] ?? [];
      if (!allowed.includes(dto.status)) {
        throw new BadRequestException({
          code: ApiErrorCode.VALIDATION_ERROR,
          message: `Cannot transition event from ${event.status} to ${dto.status}.`,
        });
      }
      data.status = dto.status;
    }
    if (Object.keys(data).length === 0) {
      return this.toView(event);
    }
    try {
      const updated = (await this.prisma.event.update({
        where: { id },
        data,
        include: {
          organizer: {
            select: { id: true, displayName: true, avatarUrl: true },
          },
        },
      })) as EventRow;
      return this.toView(updated);
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2025'
      ) {
        throw new NotFoundException({
          code: ApiErrorCode.NOT_FOUND,
          message: 'Event not found.',
        });
      }
      throw e;
    }
  }

  /** DELETE maps to CANCELLED (soft lifecycle end, never hard delete). */
  async cancel(viewerId: string, id: string): Promise<EventView> {
    const event = await this.fetchOrThrow(id);
    if (event.organizerId !== viewerId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the organizer can cancel this event.',
      });
    }
    if (event.status === 'CANCELLED') return this.toView(event);
    if (event.status === 'COMPLETED') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Completed events cannot be cancelled.',
      });
    }
    // Outbox persisted atomically with the cancellation itself.
    const updated = (await this.prisma.$transaction(async (tx) => {
      const cancelled = await tx.event.update({
        where: { id },
        data: { status: 'CANCELLED' },
        include: {
          organizer: {
            select: { id: true, displayName: true, avatarUrl: true },
          },
        },
      });
      await tx.domainEvent.create({
        data: buildOutboxEvent(
          DomainEventTypes.EVENT_CANCELLED,
          'Event',
          id,
          viewerId,
          { eventId: id, organizerId: viewerId, title: cancelled.title },
        ),
      });
      return cancelled;
    })) as EventRow;
    return this.toView(updated);
  }

  // ------------------------------------------------------------------ lists
  private visibilityOr(ctx: ViewerContext): Prisma.EventWhereInput[] {
    const branches: Prisma.EventWhereInput[] = [{ visibility: 'PUBLIC' }];
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
    return branches;
  }

  private async pagedEvents(
    ctx: ViewerContext,
    where: Prisma.EventWhereInput,
    page: ParsedPage,
    order: 'upcoming' | 'recent' = 'upcoming',
  ) {
    const orderBy =
      order === 'upcoming'
        ? [{ startsAt: 'asc' as const }, { id: 'asc' as const }]
        : [{ createdAt: 'desc' as const }, { id: 'desc' as const }];
    // Cursor encoding reuses (createdAt|id) shape; decode against startsAt here.
    const cursorWhere =
      page.cursor != null
        ? {
            AND: [
              where,
              {
                OR: [
                  { startsAt: { gt: page.cursor.createdAt } },
                  { startsAt: page.cursor.createdAt, id: { gt: page.cursor.id } },
                ],
              },
            ],
          }
        : where;
    const rows = (await this.prisma.event.findMany({
      where: cursorWhere,
      include: {
        organizer: { select: { id: true, displayName: true, avatarUrl: true } },
      },
      orderBy,
      take: page.limit + 1,
    })) as EventRow[];
    const items = rows.slice(0, page.limit).map((r) => this.toView(r));
    // Cursor over startsAt for stable upcoming pagination.
    const nextCursor =
      rows.length > page.limit
        ? encodeCursor(
            items[items.length - 1].startsAt,
            items[items.length - 1].id,
          )
        : null;
    return { items, nextCursor };
  }

  async list(
    viewerId: string,
    filters: EventListQuery,
    page: ParsedPage,
  ) {
    const ctx = await loadViewerContext(this.prisma, viewerId);
    const blockedFilter =
      ctx.blockedIds.length > 0
        ? { organizerId: { notIn: ctx.blockedIds } }
        : {};
    if (filters.mine) {
      const where: Prisma.EventWhereInput = { organizerId: viewerId };
      if (filters.status) where.status = filters.status;
      if (filters.upcoming) {
        where.startsAt = { gt: new Date() };
      }
      return this.pagedEvents(ctx, where, page);
    }
    const status = filters.status ?? 'PUBLISHED';
    if (status === 'DRAFT' || status === 'CANCELLED') {
      throw new BadRequestException({
        code: ApiErrorCode.VALIDATION_ERROR,
        message: 'Drafts and cancelled events are only listed via mine=true.',
      });
    }
    const where: Prisma.EventWhereInput = {
      status,
      ...blockedFilter,
      OR: this.visibilityOr(ctx),
    };
    if (filters.universityId) {
      if (!ctx.universityIds.includes(filters.universityId)) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'Event discovery is scoped to your universities.',
        });
      }
      where.universityId = filters.universityId;
    }
    if (filters.communityId) {
      const community = await this.prisma.community.findUnique({
        where: { id: filters.communityId },
      });
      if (!community || !community.isActive) {
        throw new NotFoundException({
          code: ApiErrorCode.NOT_FOUND,
          message: 'Community not found.',
        });
      }
      if (
        community.visibility === 'PRIVATE' &&
        !ctx.communityIds.includes(community.id)
      ) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'This community is private.',
        });
      }
      where.communityId = filters.communityId;
    }
    if (filters.upcoming) {
      where.startsAt = { gt: new Date() };
    }
    return this.pagedEvents(ctx, where, page);
  }

  async listByUniversity(viewerId: string, slug: string, page: ParsedPage) {
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
        message: 'University events are restricted to its members.',
      });
    }
    const blockedFilter =
      ctx.blockedIds.length > 0
        ? { organizerId: { notIn: ctx.blockedIds } }
        : {};
    return this.pagedEvents(
      ctx,
      {
        universityId: university.id,
        status: 'PUBLISHED',
        ...blockedFilter,
        OR: this.visibilityOr(ctx),
      },
      page,
    );
  }

  async listByCommunity(viewerId: string, communityId: string, page: ParsedPage) {
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
    const blockedFilter =
      ctx.blockedIds.length > 0
        ? { organizerId: { notIn: ctx.blockedIds } }
        : {};
    return this.pagedEvents(
      ctx,
      {
        communityId: community.id,
        status: 'PUBLISHED',
        ...blockedFilter,
        OR: this.visibilityOr(ctx),
      },
      page,
    );
  }
}

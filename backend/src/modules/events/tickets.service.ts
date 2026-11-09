import {
  ConflictException,
  ForbiddenException,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Prisma } from '@prisma/client';
import { randomBytes } from 'crypto';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  ParsedPage,
  encodeCursor,
  olderThan,
  PageResult,
} from '../../common/pagination/cursor';
import { loadViewerContext } from '../../common/access/viewer-context';
import { EventsService } from './events.service';

export interface TicketView {
  id: string;
  ticketCode: string;
  status: string;
  registeredAt: Date;
  cancelledAt: Date | null;
  checkedInAt: Date | null;
  event: {
    id: string;
    title: string;
    startsAt: Date;
    endsAt: Date | null;
    location: string | null;
    status: string;
  };
  attendee: { id: string; displayName: string | null };
  createdAt: Date;
}

/** 256-bit opaque public credential, base64url (QR-safe alphabet). */
function newTicketCode(): string {
  return randomBytes(32).toString('base64url');
}

@Injectable()
export class TicketsService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly events: EventsService,
  ) {}

  private toView(t: {
    id: string;
    ticketCode: string;
    status: string;
    registeredAt: Date;
    cancelledAt: Date | null;
    checkedInAt: Date | null;
    createdAt: Date;
    event: {
      id: string;
      title: string;
      startsAt: Date;
      endsAt: Date | null;
      location: string | null;
      status: string;
    };
    user: { id: string; displayName: string | null };
  }): TicketView {
    return {
      id: t.id,
      ticketCode: t.ticketCode,
      status: t.status,
      registeredAt: t.registeredAt,
      cancelledAt: t.cancelledAt,
      checkedInAt: t.checkedInAt,
      event: t.event,
      attendee: t.user,
      createdAt: t.createdAt,
    };
  }

  private registrationOpen(event: {
    status: string;
    registrationDeadline: Date | null;
  }): void {
    if (event.status !== 'PUBLISHED') {
      throw new ConflictException({
        code: ApiErrorCode.REGISTRATION_CLOSED,
        message:
          event.status === 'DRAFT'
            ? 'Registration is not open yet.'
            : 'Registration is closed for this event.',
      });
    }
    if (
      event.registrationDeadline &&
      event.registrationDeadline.getTime() <= Date.now()
    ) {
      throw new ConflictException({
        code: ApiErrorCode.REGISTRATION_CLOSED,
        message: 'The registration deadline has passed.',
      });
    }
  }

  // -------------------------------------------------------------- register
  async register(viewerId: string, eventId: string) {
    const event = await this.prisma.event.findUnique({
      where: { id: eventId },
      include: {
        organizer: { select: { id: true } },
      },
    });
    if (!event) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Event not found.',
      });
    }
    // Organizers may register for their own events; everyone else must pass
    // the same visibility gate as event discovery (plus block rules).
    if (event.organizerId !== viewerId) {
      const ctx = await loadViewerContext(this.prisma, viewerId);
      if (ctx.blockedIds.includes(event.organizerId)) {
        throw new ForbiddenException({
          code: ApiErrorCode.FORBIDDEN,
          message: 'You cannot register for this event.',
        });
      }
      if (
        !(await this.events.canViewEvent(ctx, event as never)) ||
        event.status !== 'PUBLISHED'
      ) {
        // Hide non-discoverable events: 404, not 403.
        if (event.status !== 'PUBLISHED') {
          throw new ConflictException({
            code: ApiErrorCode.REGISTRATION_CLOSED,
            message: 'Registration is not open for this event.',
          });
        }
        throw new NotFoundException({
          code: ApiErrorCode.NOT_FOUND,
          message: 'Event not found.',
        });
      }
    }
    this.registrationOpen(event);

    // Row-locked transaction: concurrent registrations for one event are
    // serialized on the event row, so capacity can never be exceeded and
    // duplicate rows can never be created.
    try {
      const result = await this.prisma.$transaction(async (tx) => {
        const locked = await tx.$queryRaw<
          Array<{
            id: string;
            status: string;
            registeredCount: number;
            capacity: number;
            registrationDeadline: Date | null;
          }>
        >`SELECT id, status, "registeredCount", capacity, "registrationDeadline" FROM "Event" WHERE id = ${eventId} FOR UPDATE`;
        const row = locked[0];
        if (!row) {
          throw new NotFoundException({
            code: ApiErrorCode.NOT_FOUND,
            message: 'Event not found.',
          });
        }
        if (row.status !== 'PUBLISHED') {
          throw new ConflictException({
            code: ApiErrorCode.REGISTRATION_CLOSED,
            message: 'Registration is not open for this event.',
          });
        }
        if (
          row.registrationDeadline &&
          new Date(row.registrationDeadline).getTime() <= Date.now()
        ) {
          throw new ConflictException({
            code: ApiErrorCode.REGISTRATION_CLOSED,
            message: 'The registration deadline has passed.',
          });
        }
        const existing = await tx.ticket.findUnique({
          where: { eventId_userId: { eventId, userId: viewerId } },
        });
        if (
          existing &&
          (existing.status === 'ACTIVE' || existing.status === 'CHECKED_IN')
        ) {
          return { ticketId: existing.id, created: false };
        }
        if (row.registeredCount >= row.capacity) {
          throw new ConflictException({
            code: ApiErrorCode.EVENT_FULL,
            message: 'This event is full.',
          });
        }
        let ticketId: string;
        if (existing) {
          // Reactivate a previously cancelled registration (same stable code).
          const re = await tx.ticket.update({
            where: { id: existing.id },
            data: {
              status: 'ACTIVE',
              cancelledAt: null,
              registeredAt: new Date(),
            },
          });
          ticketId = re.id;
        } else {
          const created = await tx.ticket.create({
            data: {
              eventId,
              userId: viewerId,
              ticketCode: newTicketCode(),
              status: 'ACTIVE',
            },
          });
          ticketId = created.id;
        }
        await tx.event.update({
          where: { id: eventId },
          data: { registeredCount: { increment: 1 } },
        });
        return { ticketId, created: !existing };
      });

      const ticket = await this.prisma.ticket.findUniqueOrThrow({
        where: { id: result.ticketId },
        include: {
          event: {
            select: {
              id: true,
              title: true,
              startsAt: true,
              endsAt: true,
              location: true,
              status: true,
            },
          },
          user: { select: { id: true, displayName: true } },
        },
      });
      return { ticket: this.toView(ticket), created: result.created };
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        // Lost a concurrent create race: return the winner idempotently.
        const winner = await this.prisma.ticket.findUniqueOrThrow({
          where: { eventId_userId: { eventId, userId: viewerId } },
          include: {
            event: {
              select: {
                id: true,
                title: true,
                startsAt: true,
                endsAt: true,
                location: true,
                status: true,
              },
            },
            user: { select: { id: true, displayName: true } },
          },
        });
        return { ticket: this.toView(winner), created: false };
      }
      throw e;
    }
  }

  // ---------------------------------------------------------------- cancel
  async cancelRegistration(
    viewerId: string,
    eventId: string,
  ): Promise<{ cancelled: boolean }> {
    const ticket = await this.prisma.ticket.findUnique({
      where: { eventId_userId: { eventId, userId: viewerId } },
    });
    if (!ticket || ticket.status !== 'ACTIVE') {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'No active registration found.',
      });
    }
    await this.prisma.$transaction([
      this.prisma.ticket.update({
        where: { id: ticket.id },
        data: { status: 'CANCELLED', cancelledAt: new Date() },
      }),
      this.prisma.event.update({
        where: { id: eventId },
        data: { registeredCount: { decrement: 1 } },
      }),
    ]);
    return { cancelled: true };
  }

  // ------------------------------------------------------------- my tickets
  async myTickets(
    viewerId: string,
    page: ParsedPage,
  ): Promise<PageResult<TicketView>> {
    const where = { userId: viewerId };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.ticket.findMany({
      where: cursorWhere,
      include: {
        event: {
          select: {
            id: true,
            title: true,
            startsAt: true,
            endsAt: true,
            location: true,
            status: true,
          },
        },
        user: { select: { id: true, displayName: true } },
      },
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    // Reuse the generic slicer shape manually (TicketView lacks createdAt-first order needs).
    const items = rows.slice(0, page.limit).map((t) => this.toView(t));
    const nextCursor =
      rows.length > page.limit
        ? encodeCursor(rows[page.limit - 1].createdAt, rows[page.limit - 1].id)
        : null;
    return { items, nextCursor };
  }

  // ---------------------------------------------------------------- detail
  async detail(viewerId: string, id: string): Promise<TicketView> {
    const ticket = await this.prisma.ticket.findUnique({
      where: { id },
      include: {
        event: {
          select: {
            id: true,
            title: true,
            startsAt: true,
            endsAt: true,
            location: true,
            status: true,
            organizerId: true,
          },
        },
        user: { select: { id: true, displayName: true } },
      },
    });
    if (!ticket) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Ticket not found.',
      });
    }
    const isOwner = ticket.userId === viewerId;
    const isOrganizer = ticket.event.organizerId === viewerId;
    if (!isOwner && !isOrganizer) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Ticket not found.',
      });
    }
    const { organizerId: _organizerId, ...event } = ticket.event;
    void _organizerId;
    return this.toView({ ...ticket, event });
  }

  // ---------------------------------------------------------------- verify
  async verify(
    verifierId: string,
    eventId: string,
    ticketCode: string,
  ): Promise<TicketView & { checkedIn: boolean }> {
    const event = await this.prisma.event.findUnique({
      where: { id: eventId },
    });
    if (!event) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Event not found.',
      });
    }
    // Authorization first: the scanner must organize this event. This order
    // also prevents using verification as a ticket oracle.
    if (event.organizerId !== verifierId) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'Only the event organizer can verify tickets.',
      });
    }
    if (event.status !== 'PUBLISHED') {
      throw new ConflictException({
        code: ApiErrorCode.REGISTRATION_CLOSED,
        message: 'This event is not open for check-in.',
      });
    }
    const ticket = await this.prisma.ticket.findUnique({
      where: { ticketCode },
    });
    // Cross-event codes are rejected generically: no information leaks about
    // any other event.
    if (!ticket || ticket.eventId !== eventId) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'Ticket not found for this event.',
      });
    }
    if (ticket.status === 'CHECKED_IN') {
      throw new ConflictException({
        code: ApiErrorCode.CONFLICT,
        message: 'Ticket has already been checked in.',
      });
    }
    if (ticket.status !== 'ACTIVE') {
      throw new ConflictException({
        code: ApiErrorCode.CONFLICT,
        message: 'Ticket is not active.',
      });
    }
    // Exactly one concurrent scan may transition ACTIVE → CHECKED_IN.
    const claimed = await this.prisma.ticket.updateMany({
      where: { id: ticket.id, status: 'ACTIVE' },
      data: { status: 'CHECKED_IN', checkedInAt: new Date() },
    });
    if (claimed.count !== 1) {
      throw new ConflictException({
        code: ApiErrorCode.CONFLICT,
        message: 'Ticket has already been checked in.',
      });
    }
    const full = await this.detail(verifierId, ticket.id);
    return { ...full, checkedIn: true };
  }
}

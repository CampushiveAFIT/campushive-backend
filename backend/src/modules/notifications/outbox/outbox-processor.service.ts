import { Injectable, Logger, OnModuleDestroy, OnModuleInit } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Prisma } from '@prisma/client';
import { PrismaService } from '../../../config/prisma.service';
import { MessageEventsHandler } from '../../messaging/message-events.handler';
import { NotificationHandlers } from './notification-handlers.service';
import {
  OUTBOX_BATCH_SIZE,
  OUTBOX_MAX_ATTEMPTS,
  retryDelayMs,
} from './domain-event';

/**
 * Transactional-outbox poller. Claims due PENDING rows with a conditional
 * update (exactly one claimer wins, even across workers), dispatches each
 * to NotificationHandlers, then marks DONE — or re-arms with backoff, and
 * finally FAILED after OUTBOX_MAX_ATTEMPTS attempts.
 *
 * Interval-driven in app runtime; tests drive it deterministically via
 * drain(). Handler-level dedupeKey keeps redelivery safe.
 */
@Injectable()
export class OutboxProcessorService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(OutboxProcessorService.name);
  private timer: NodeJS.Timeout | null = null;
  private running = false;

  constructor(
    private readonly prisma: PrismaService,
    private readonly handlers: NotificationHandlers,
    private readonly messageEvents: MessageEventsHandler,
    private readonly config: ConfigService,
  ) {}

  onModuleInit(): void {
    const ms = this.pollMs();
    this.timer = setInterval(() => {
      void this.drain().catch((e) =>
        this.logger.error(`outbox drain failed: ${(e as Error).message}`),
      );
    }, ms);
    // Don't hold the process open for the poller alone (tests manage lifecycle).
    if (typeof this.timer.unref === 'function') this.timer.unref();
  }

  onModuleDestroy(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  private pollMs(): number {
    const raw = process.env.OUTBOX_POLL_MS ?? '2000';
    const n = parseInt(raw, 10);
    return Number.isFinite(n) && n >= 250 ? n : 2000;
  }

  /** Process all currently-due events. Safe to call concurrently. */
  async drain(): Promise<{ processed: number; failed: number }> {
    if (this.running) return { processed: 0, failed: 0 };
    this.running = true;
    let processed = 0;
    let failed = 0;
    try {
      // Crash recovery: claims stuck longer than 5 minutes (worker died
      // mid-processing) return to the due pool without extra attempts.
      await this.prisma.domainEvent.updateMany({
        where: {
          status: 'PROCESSING',
          updatedAt: { lt: new Date(Date.now() - 5 * 60 * 1000) },
        },
        data: { status: 'PENDING' },
      });
      for (;;) {
        const claimed = await this.claimBatch();
        if (claimed.length === 0) break;
        for (const event of claimed) {
          try {
            // message.* events drive realtime delivery; everything else
            // drives notification creation. Routing is by type prefix.
            if (event.type.startsWith('message.')) {
              await this.messageEvents.dispatch(event.type, {
                id: event.id,
                actorId: event.actorId,
                payload: event.payload,
              });
            } else {
              await this.handlers.dispatch(event.type, {
                id: event.id,
                actorId: event.actorId,
                payload: event.payload,
              });
            }
            await this.prisma.domainEvent.update({
              where: { id: event.id },
              data: { status: 'DONE', processedAt: new Date(), lastError: null },
            });
            processed += 1;
          } catch (err) {
            const attempts = event.attempts + 1;
            const message = (err as Error).message.slice(0, 1000);
            if (attempts >= OUTBOX_MAX_ATTEMPTS) {
              await this.prisma.domainEvent.update({
                where: { id: event.id },
                data: { status: 'FAILED', lastError: message },
              });
              failed += 1;
              this.logger.error(
                `outbox event ${event.id} failed permanently: ${message}`,
              );
            } else {
              await this.prisma.domainEvent.update({
                where: { id: event.id },
                data: {
                  status: 'PENDING',
                  attempts,
                  availableAt: new Date(Date.now() + retryDelayMs(attempts)),
                  lastError: message,
                },
              });
            }
          }
        }
        if (claimed.length < OUTBOX_BATCH_SIZE) break;
      }
    } finally {
      this.running = false;
    }
    return { processed, failed };
  }

  /** Conditional claim: only unclaimed, due rows flip to PROCESSING. */
  private async claimBatch() {
    const now = new Date();
    const due = await this.prisma.domainEvent.findMany({
      where: { status: 'PENDING', availableAt: { lte: now } },
      orderBy: { createdAt: 'asc' },
      take: OUTBOX_BATCH_SIZE,
      select: { id: true },
    });
    const claimed: Array<{
      id: string;
      type: string;
      actorId: string | null;
      payload: Prisma.JsonValue;
      attempts: number;
    }> = [];
    for (const row of due) {
      const res = await this.prisma.domainEvent.updateMany({
        where: { id: row.id, status: 'PENDING' },
        data: { status: 'PROCESSING', attempts: { increment: 1 } },
      });
      if (res.count !== 1) continue; // lost the race to another claimer
      const full = await this.prisma.domainEvent.findUnique({
        where: { id: row.id },
      });
      if (full && full.status === 'PROCESSING') claimed.push(full);
    }
    return claimed;
  }
}

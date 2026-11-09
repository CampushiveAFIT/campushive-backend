import { Logger } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { JwtService } from '@nestjs/jwt';
import {
  OnGatewayConnection,
  OnGatewayDisconnect,
  WebSocketGateway,
  WebSocketServer,
} from '@nestjs/websockets';
import { Server, Socket } from 'socket.io';
import { verifySocketUser } from '../../../common/realtime/socket-auth';

export const NOTIFICATION_CREATED_EVENT = 'notification.created';
export const userRoom = (userId: string): string => `user:${userId}`;

/**
 * Realtime delivery foundation. Server-emit only: clients join their own
 * per-user room after a JWT handshake and receive `notification.created`
 * payloads. No client-to-server messaging exists in this slice (future
 * messaging will reuse this gateway, rooms, and auth pattern).
 */
@WebSocketGateway({
  cors: true,
  transports: ['websocket', 'polling'],
})
export class NotificationsGateway
  implements OnGatewayConnection, OnGatewayDisconnect
{
  @WebSocketServer()
  private server!: Server;

  private readonly logger = new Logger(NotificationsGateway.name);

  constructor(
    private readonly jwt: JwtService,
    private readonly config: ConfigService,
  ) {}

  async handleConnection(client: Socket): Promise<void> {
    // Origin allowlist is enforced here (runtime config) rather than in the
    // decorator, whose options evaluate at import time before .env loads.
    const allowed = this.config
      .get<string[]>('corsOrigins', [])
      .map((o) => o.toLowerCase());
    const origin = client.handshake.headers.origin;
    if (typeof origin === 'string' && allowed.length > 0 && !allowed.includes(origin.toLowerCase())) {
      client.disconnect(true);
      return;
    }
    const userId = await verifySocketUser(this.jwt, this.config, client);
    if (!userId) {
      this.logger.debug('rejected socket connection: invalid credentials');
      client.disconnect(true);
      return;
    }
    client.data.userId = userId;
    await client.join(userRoom(userId));
  }

  handleDisconnect(client: Socket): void {
    // Rooms are cleaned up automatically by socket.io on disconnect.
    void client;
  }

  /** Best-effort server-side emit; delivery is not acknowledged. */
  emitToUser(userId: string, payload: unknown): void {
    try {
      this.server?.to(userRoom(userId)).emit(NOTIFICATION_CREATED_EVENT, payload);
    } catch (err) {
      this.logger.warn(
        `realtime emit failed for ${userId}: ${(err as Error).message}`,
      );
    }
  }
}

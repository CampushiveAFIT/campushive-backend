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
import { userRoom } from '../../notifications/realtime/notifications.gateway';

export const MESSAGE_CREATED_EVENT = 'message.created';
export const MESSAGE_UPDATED_EVENT = 'message.updated';
export const MESSAGE_DELETED_EVENT = 'message.deleted';

/**
 * Realtime delivery for 1:1 messaging. Server-emit only: authenticated
 * sockets join their private `user:{id}` room (shared with notifications so
 * one client connection serves both) and receive message events. Identity
 * always derives from the verified JWT — never from client payloads.
 */
@WebSocketGateway({
  cors: true,
  transports: ['websocket', 'polling'],
})
export class MessagingGateway implements OnGatewayConnection, OnGatewayDisconnect {
  @WebSocketServer()
  private server!: Server;

  private readonly logger = new Logger(MessagingGateway.name);

  constructor(
    private readonly jwt: JwtService,
    private readonly config: ConfigService,
  ) {}

  async handleConnection(client: Socket): Promise<void> {
    const allowed = this.config
      .get<string[]>('corsOrigins', [])
      .map((o) => o.toLowerCase());
    const origin = client.handshake.headers.origin;
    if (
      typeof origin === 'string' &&
      allowed.length > 0 &&
      !allowed.includes(origin.toLowerCase())
    ) {
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
    void client;
  }

  /** Best-effort server-side emit to one user's room. */
  emitToUser(userId: string, event: string, payload: unknown): void {
    try {
      this.server?.to(userRoom(userId)).emit(event, payload);
    } catch (err) {
      this.logger.warn(
        `realtime emit failed for ${userId}: ${(err as Error).message}`,
      );
    }
  }
}

import { JwtService } from '@nestjs/jwt';
import { ConfigService } from '@nestjs/config';
import { Socket } from 'socket.io';

/**
 * Shared Socket.IO handshake authentication. Verifies the Bearer access JWT
 * from `auth.token` (or `?token=`), returning the user id, or null when the
 * socket must be disconnected. Single implementation used by every gateway.
 */
export async function verifySocketUser(
  jwt: JwtService,
  config: ConfigService,
  client: Socket,
): Promise<string | null> {
  const token =
    (client.handshake.auth?.token as string | undefined) ??
    (typeof client.handshake.query?.token === 'string'
      ? client.handshake.query.token
      : undefined);
  if (!token) return null;
  try {
    const payload = await jwt.verifyAsync<{
      sub?: unknown;
      jti?: unknown;
    }>(token, { secret: config.get<string>('jwt.accessSecret') });
    if (typeof payload?.sub !== 'string' || typeof payload?.jti !== 'string') {
      return null;
    }
    return payload.sub;
  } catch {
    return null;
  }
}

/** CORS origins shared by all gateways (runtime config, never `*`). */
export function socketCorsOrigins(config: ConfigService): string[] {
  return config.get<string[]>('corsOrigins', []);
}

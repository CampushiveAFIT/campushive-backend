import {
  Inject,
  Injectable,
  Logger,
  Module,
  OnModuleDestroy,
  OnModuleInit,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import Redis from 'ioredis';
import * as fs from 'fs';
import * as path from 'path';

export const REDIS_CLIENT = 'REDIS_CLIENT';

/**
 * Provides a single shared ioredis client.
 *
 * - Production: REDIS_URL is required (fail fast at startup otherwise).
 * - Development/test without REDIS_URL: starts an embedded real Redis
 *   server (redis-memory-server) on a random port. This is a genuine
 *   Redis server binary, not a mock, so Lua atomicity semantics hold.
 */
@Injectable()
export class RedisService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(RedisService.name);
  private embedded: { stop: () => Promise<boolean> } | null = null;

  constructor(
    @Inject(REDIS_CLIENT) private readonly client: Redis,
    private readonly config: ConfigService,
  ) {}

  getClient(): Redis {
    return this.client;
  }

  async onModuleInit(): Promise<void> {
    // Connection is established lazily by the factory; nothing to do here.
  }

  async onModuleDestroy(): Promise<void> {
    try {
      this.client.disconnect();
    } catch {
      /* ignore */
    }
    const embedded =
      this.embedded ??
      (this.client as unknown as { __embedded?: { stop: () => Promise<boolean> } })
        .__embedded ??
      null;
    if (embedded) {
      try {
        await embedded.stop();
      } catch {
        /* ignore */
      }
      this.embedded = null;
    }
  }

  setEmbedded(server: { stop: () => Promise<boolean> }): void {
    this.embedded = server;
  }
}

/** Wait for the client 'ready' event (bounded); rejects on timeout/error. */
async function waitReady(client: Redis, timeoutMs: number): Promise<void> {
  if (client.status === 'ready') return;
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new Error(`Redis did not become ready within ${timeoutMs}ms`));
    }, timeoutMs);
    client.once('ready', () => {
      clearTimeout(timer);
      resolve();
    });
    client.once('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });
}

@Module({
  providers: [
    {
      provide: REDIS_CLIENT,
      inject: [ConfigService],
      useFactory: async (config: ConfigService): Promise<Redis> => {
        const logger = new Logger('RedisModule');
        const redisUrl = config.get<string>('redisUrl', '');
        const nodeEnv = config.get<string>('nodeEnv', 'development');

        if (redisUrl) {
          logger.log('Connecting to Redis via REDIS_URL');
          const client = new Redis(redisUrl, {
            enableOfflineQueue: false,
            maxRetriesPerRequest: 2,
            lazyConnect: true,
          });
          await client.connect().catch(() => undefined);
          await waitReady(client, 15000);
          logger.log('Redis connection established (external)');
          return client;
        }

        if (nodeEnv === 'production') {
          throw new Error(
            'REDIS_URL is required in production. Refusing to start without rate-limit storage.',
          );
        }

        // Development/test fallback: embedded real Redis server binary.
        const localBinary = path.resolve(process.cwd(), 'bin', 'redis-server');
        if (fs.existsSync(localBinary)) {
          process.env.REDISMS_SYSTEM_BINARY = localBinary;
        }
        logger.log(
          'REDIS_URL unset: starting embedded Redis server (development/test only)',
        );
        // eslint-disable-next-line @typescript-eslint/no-require-imports
        const { RedisMemoryServer } = require('redis-memory-server') as typeof import('redis-memory-server');
        const server = new RedisMemoryServer();
        const host = await server.getHost();
        const port = await server.getPort();
        const client = new Redis(port, host, {
          enableOfflineQueue: false,
          maxRetriesPerRequest: 2,
          lazyConnect: true,
        });
        await client.connect().catch(() => undefined);
        await waitReady(client, 30000);
        logger.log(`Embedded Redis running at ${host}:${port}`);
        // Stash the server handle on the client for lifecycle cleanup.
        (client as unknown as { __embedded: unknown }).__embedded = server;
        return client;
      },
    },
    RedisService,
  ],
  exports: [REDIS_CLIENT, RedisService],
})
export class RedisModule {}

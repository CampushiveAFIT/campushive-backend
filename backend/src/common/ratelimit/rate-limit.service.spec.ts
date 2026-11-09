import Redis from 'ioredis';
import { RateLimitService } from './rate-limit.service';

describe('RateLimitService', () => {
  it('fails open (allows) when Redis is unreachable', async () => {
    const dead = new Redis({ port: 1, host: '127.0.0.1', lazyConnect: true, maxRetriesPerRequest: 1, enableOfflineQueue: false, retryStrategy: () => null });
    dead.on('error', () => undefined);
    const svc = new RateLimitService(dead);
    const res = await svc.hit('rl:test:dead', 1, 60);
    expect(res.allowed).toBe(true);
    dead.disconnect();
  });
});

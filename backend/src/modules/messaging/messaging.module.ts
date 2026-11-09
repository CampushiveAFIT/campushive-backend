import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import { RedisModule } from '../../common/redis/redis.module';
import { RateLimitService } from '../../common/ratelimit/rate-limit.service';
import { RateLimitGuard } from '../../common/ratelimit/rate-limit.guard';
import { ConversationsController } from './conversations.controller';
import { MessagesController } from './messages.controller';
import { ConversationsService } from './conversations.service';
import { MessagesService } from './messages.service';
import { MessageEventsHandler } from './message-events.handler';
import { MessagingGateway } from './realtime/messaging.gateway';

@Module({
  imports: [PrismaModule, RedisModule, JwtModule.register({})],
  controllers: [ConversationsController, MessagesController],
  providers: [
    ConversationsService,
    MessagesService,
    MessageEventsHandler,
    MessagingGateway,
    RateLimitService,
    RateLimitGuard,
  ],
  exports: [ConversationsService, MessagesService, MessageEventsHandler],
})
export class MessagingModule {}

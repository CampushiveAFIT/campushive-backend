import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import {
  NotificationPreferencesController,
  NotificationsController,
} from './notifications.controller';
import { NotificationsService } from './notifications.service';
import { NotificationHandlers } from './outbox/notification-handlers.service';
import { OutboxProcessorService } from './outbox/outbox-processor.service';
import { NotificationsGateway } from './realtime/notifications.gateway';
import { MessagingModule } from '../messaging/messaging.module';

@Module({
  imports: [PrismaModule, JwtModule.register({}), MessagingModule],
  controllers: [NotificationsController, NotificationPreferencesController],
  providers: [
    NotificationsService,
    NotificationHandlers,
    OutboxProcessorService,
    NotificationsGateway,
  ],
  exports: [NotificationsService, NotificationHandlers, OutboxProcessorService],
})
export class NotificationsModule {}

import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import {
  CommunityEventsController,
  EventsController,
  UniversityEventsController,
} from './events.controller';
import {
  EventTicketsController,
  TicketsController,
  UserTicketsController,
} from './tickets.controller';
import { EventsService } from './events.service';
import { TicketsService } from './tickets.service';

@Module({
  imports: [PrismaModule, JwtModule.register({})],
  controllers: [
    EventsController,
    UniversityEventsController,
    CommunityEventsController,
    EventTicketsController,
    TicketsController,
    UserTicketsController,
  ],
  providers: [EventsService, TicketsService],
  exports: [EventsService, TicketsService],
})
export class EventsModule {}

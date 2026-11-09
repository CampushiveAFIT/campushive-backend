import {
  Body,
  Controller,
  Delete,
  Get,
  HttpCode,
  HttpStatus,
  Param,
  ParseUUIDPipe,
  Post,
  Query,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { TicketsService } from './tickets.service';
import { VerifyTicketDto } from './dto/event.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('tickets')
@ApiBearerAuth('access-token')
@Controller('events')
@UseGuards(JwtAccessGuard)
export class EventTicketsController {
  constructor(private readonly tickets: TicketsService) {}

  @Post(':id/register')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Register for a free event (idempotent)' })
  async register(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.tickets.register(user.sub, id);
  }

  @Delete(':id/registration')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Cancel own registration' })
  async cancel(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.tickets.cancelRegistration(user.sub, id);
  }

  @Post(':id/tickets/verify')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Verify/check-in a ticket (organizer only)' })
  async verify(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: VerifyTicketDto,
  ) {
    return this.tickets.verify(user.sub, id, dto.ticketCode);
  }
}

@ApiTags('tickets')
@ApiBearerAuth('access-token')
@Controller('tickets')
@UseGuards(JwtAccessGuard)
export class TicketsController {
  constructor(private readonly tickets: TicketsService) {}

  @Get(':id')
  @ApiOperation({ summary: 'Ticket detail (owner or organizer)' })
  async detail(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.tickets.detail(user.sub, id);
  }
}

@ApiTags('tickets')
@ApiBearerAuth('access-token')
@Controller('users')
@UseGuards(JwtAccessGuard)
export class UserTicketsController {
  constructor(private readonly tickets: TicketsService) {}

  @Get('me/tickets')
  @ApiOperation({ summary: "List the caller's tickets" })
  async mine(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.tickets.myTickets(user.sub, parsePage(query));
  }
}

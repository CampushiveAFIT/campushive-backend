import {
  Body,
  Controller,
  Delete,
  Get,
  HttpCode,
  HttpStatus,
  Param,
  ParseUUIDPipe,
  Patch,
  Post,
  Query,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { EventsService } from './events.service';
import {
  CreateEventDto,
  EventListQuery,
  UpdateEventDto,
} from './dto/event.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('events')
@ApiBearerAuth('access-token')
@Controller('events')
@UseGuards(JwtAccessGuard)
export class EventsController {
  constructor(private readonly events: EventsService) {}

  @Post()
  @ApiOperation({ summary: 'Create an event (starts as DRAFT)' })
  async create(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: CreateEventDto,
  ) {
    return this.events.create(user.sub, dto);
  }

  @Get()
  @ApiOperation({ summary: 'Discover events (visibility-enforced)' })
  async list(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: EventListQuery,
  ) {
    const page = parsePage({ limit: query.limit, cursor: query.cursor });
    return this.events.list(user.sub, query, page);
  }

  @Get(':id')
  @ApiOperation({ summary: 'Event detail (visibility-enforced)' })
  async detail(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.events.detail(user.sub, id);
  }

  @Patch(':id')
  @ApiOperation({ summary: 'Update own event (organizer only)' })
  async update(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: UpdateEventDto,
  ) {
    return this.events.update(user.sub, id, dto);
  }

  @Delete(':id')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Cancel own event (organizer only)' })
  async cancel(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.events.cancel(user.sub, id);
  }
}

@ApiTags('events')
@ApiBearerAuth('access-token')
@Controller('universities')
@UseGuards(JwtAccessGuard)
export class UniversityEventsController {
  constructor(private readonly events: EventsService) {}

  @Get(':slug/events')
  @ApiOperation({ summary: 'List university events (members only)' })
  async byUniversity(
    @CurrentUser() user: AccessTokenPayload,
    @Param('slug') slug: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.events.listByUniversity(user.sub, slug, parsePage(query));
  }
}

@ApiTags('events')
@ApiBearerAuth('access-token')
@Controller('communities')
@UseGuards(JwtAccessGuard)
export class CommunityEventsController {
  constructor(private readonly events: EventsService) {}

  @Get(':id/events')
  @ApiOperation({ summary: 'List community events' })
  async byCommunity(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.events.listByCommunity(user.sub, id, parsePage(query));
  }
}

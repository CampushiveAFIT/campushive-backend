import {
  Body,
  Controller,
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
import { NotificationsService } from './notifications.service';
import { UpdatePreferencesDto } from './dto/preferences.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('notifications')
@ApiBearerAuth('access-token')
@Controller('notifications')
@UseGuards(JwtAccessGuard)
export class NotificationsController {
  constructor(private readonly notifications: NotificationsService) {}

  @Get()
  @ApiOperation({ summary: 'List my notifications (newest first)' })
  async list(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: { limit?: unknown; cursor?: unknown; unread?: unknown },
  ) {
    const unreadOnly = query.unread === true || query.unread === 'true';
    return this.notifications.list(user.sub, parsePage(query), unreadOnly);
  }

  @Get('unread-count')
  @ApiOperation({ summary: 'Count my unread notifications' })
  async unreadCount(@CurrentUser() user: AccessTokenPayload) {
    return this.notifications.unreadCount(user.sub);
  }

  @Post('read-all')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Mark all my notifications as read' })
  async readAll(@CurrentUser() user: AccessTokenPayload) {
    return this.notifications.markAllRead(user.sub);
  }

  @Post(':id/read')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Mark one notification as read (idempotent)' })
  async read(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.notifications.markRead(user.sub, id);
  }
}

@ApiTags('users')
@ApiBearerAuth('access-token')
@Controller('users')
@UseGuards(JwtAccessGuard)
export class NotificationPreferencesController {
  constructor(private readonly notifications: NotificationsService) {}

  @Get('me/notification-preferences')
  @ApiOperation({ summary: 'Get my notification preferences (defaults if unset)' })
  async get(@CurrentUser() user: AccessTokenPayload) {
    return this.notifications.getPreferences(user.sub);
  }

  @Patch('me/notification-preferences')
  @ApiOperation({ summary: 'Update my notification preferences' })
  async update(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: UpdatePreferencesDto,
  ) {
    return this.notifications.updatePreferences(user.sub, dto);
  }
}

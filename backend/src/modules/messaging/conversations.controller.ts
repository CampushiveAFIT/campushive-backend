import {
  Body,
  Controller,
  Get,
  HttpCode,
  HttpStatus,
  Param,
  ParseUUIDPipe,
  Post,
  Query,
  Res,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { ConversationsService } from './conversations.service';
import { MessagesService } from './messages.service';
import {
  CreateDirectConversationDto,
  SendMessageDto,
} from './dto/messaging.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { RateLimit } from '../../common/ratelimit/rate-limit.decorator';
import { RateLimitGuard } from '../../common/ratelimit/rate-limit.guard';
import { parsePage } from '../../common/pagination/cursor';
import type { Response } from 'express';

@ApiTags('messaging')
@ApiBearerAuth('access-token')
@Controller('conversations')
@UseGuards(JwtAccessGuard)
export class ConversationsController {
  constructor(
    private readonly conversations: ConversationsService,
    private readonly messages: MessagesService,
  ) {}

  @Post('direct')
  @ApiOperation({ summary: 'Get or create a 1:1 conversation' })
  async direct(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: CreateDirectConversationDto,
    @Res({ passthrough: true }) res: Response,
  ) {
    const result = await this.conversations.getOrCreateDirect(user.sub, dto.userId);
    res.status(result.created ? HttpStatus.CREATED : HttpStatus.OK);
    return result.conversation;
  }

  @Get()
  @ApiOperation({ summary: 'List my conversations (recent first)' })
  async list(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.conversations.list(user.sub, parsePage(query));
  }

  @Get('unread-count')
  @ApiOperation({ summary: 'Total unread messages across conversations' })
  async unreadCount(@CurrentUser() user: AccessTokenPayload) {
    return this.conversations.unreadTotal(user.sub);
  }

  @Get(':id')
  @ApiOperation({ summary: 'Conversation detail (members only)' })
  async detail(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.conversations.project(user.sub, id);
  }

  @Get(':id/messages')
  @ApiOperation({ summary: 'List conversation messages (newest first)' })
  async messagesList(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.messages.list(user.sub, id, parsePage(query));
  }

  @Post(':id/messages')
  @UseGuards(RateLimitGuard)
  @RateLimit({ limit: 30, windowSec: 60, key: 'messages', scope: 'user' })
  @ApiOperation({ summary: 'Send a message (idempotent via clientMessageId)' })
  async send(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: SendMessageDto,
    @Res({ passthrough: true }) res: Response,
  ) {
    const result = await this.messages.send(user.sub, id, dto);
    // Idempotent replay returns the original with 200; only a fresh
    // message is 201.
    res.status(result.created ? HttpStatus.CREATED : HttpStatus.OK);
    return result.message;
  }

  @Post(':id/read')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Mark conversation as read' })
  async read(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.messages.markRead(user.sub, id);
  }
}

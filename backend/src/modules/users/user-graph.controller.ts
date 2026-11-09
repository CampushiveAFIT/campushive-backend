import {
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
import { UserGraphService } from './user-graph.service';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('users')
@ApiBearerAuth('access-token')
@Controller('users')
@UseGuards(JwtAccessGuard)
export class UserGraphController {
  constructor(private readonly graph: UserGraphService) {}

  @Post(':id/follow')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Follow a user (idempotent)' })
  async follow(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.graph.follow(user.sub, id);
  }

  @Delete(':id/follow')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Unfollow a user (idempotent)' })
  async unfollow(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.graph.unfollow(user.sub, id);
  }

  @Get(':id/followers')
  @ApiOperation({ summary: 'List followers of a user' })
  async followers(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.graph.followers(user.sub, id, parsePage(query));
  }

  @Get(':id/following')
  @ApiOperation({ summary: 'List users a user follows' })
  async following(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.graph.following(user.sub, id, parsePage(query));
  }

  @Post(':id/block')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Block a user (severs follows both ways)' })
  async block(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.graph.block(user.sub, id);
  }

  @Delete(':id/block')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Unblock a user (idempotent)' })
  async unblock(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.graph.unblock(user.sub, id);
  }
}

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
import { CommunitiesService } from './communities.service';
import {
  CreateCommunityDto,
  UpdateCommunityDto,
  UpdateMemberRoleDto,
} from './dto/community.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('communities')
@ApiBearerAuth('access-token')
@Controller('communities')
@UseGuards(JwtAccessGuard)
export class CommunitiesController {
  constructor(private readonly communities: CommunitiesService) {}

  @Post()
  @ApiOperation({ summary: 'Create a community in your own university' })
  async create(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: CreateCommunityDto,
  ) {
    return this.communities.create(user.sub, dto);
  }

  @Get(':id')
  @ApiOperation({ summary: 'Community detail (visibility-enforced)' })
  async detail(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.communities.detail(user.sub, id);
  }

  @Patch(':id')
  @ApiOperation({ summary: 'Update a community (owner only)' })
  async update(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: UpdateCommunityDto,
  ) {
    return this.communities.update(user.sub, id, dto);
  }

  @Delete(':id')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Deactivate a community (owner only)' })
  async deactivate(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.communities.deactivate(user.sub, id);
  }

  @Post(':id/join')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Join a community' })
  async join(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.communities.join(user.sub, id);
  }

  @Delete(':id/leave')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Leave a community' })
  async leave(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.communities.leave(user.sub, id);
  }

  @Get(':id/members')
  @ApiOperation({ summary: 'List community members' })
  async members(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.communities.listMembers(user.sub, id, parsePage(query));
  }

  @Patch(':id/members/:userId')
  @ApiOperation({ summary: 'Change a member role (owner only)' })
  async updateRole(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Param('userId', ParseUUIDPipe) userId: string,
    @Body() dto: UpdateMemberRoleDto,
  ) {
    return this.communities.updateMemberRole(user.sub, id, userId, dto.role);
  }

  @Delete(':id/members/:userId')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Remove (ban) a member (owner/moderator)' })
  async remove(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Param('userId', ParseUUIDPipe) userId: string,
  ) {
    return this.communities.removeMember(user.sub, id, userId);
  }
}

@ApiTags('universities')
@ApiBearerAuth('access-token')
@Controller('universities')
@UseGuards(JwtAccessGuard)
export class UniversityCommunitiesController {
  constructor(private readonly communities: CommunitiesService) {}

  @Get(':slug/communities')
  @ApiOperation({ summary: 'List communities of a university' })
  async byUniversity(
    @CurrentUser() user: AccessTokenPayload,
    @Param('slug') slug: string,
    @Query() query: { limit?: unknown; cursor?: unknown },
  ) {
    return this.communities.listByUniversity(slug, user.sub, parsePage(query));
  }
}

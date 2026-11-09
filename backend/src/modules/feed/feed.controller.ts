import {
  Controller,
  Get,
  Param,
  ParseUUIDPipe,
  Query,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { HomeFeedService } from './services/home-feed.service';
import { UniversityFeedService } from './services/university-feed.service';
import { CommunityFeedService } from './services/community-feed.service';
import { ExploreFeedService } from './services/explore-feed.service';
import {
  FeedQueryDto,
  UniversityFeedQueryDto,
} from './dto/feed-query.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';

@ApiTags('feed')
@ApiBearerAuth('access-token')
@Controller('feed')
@UseGuards(JwtAccessGuard)
export class FeedController {
  constructor(
    private readonly home: HomeFeedService,
    private readonly university: UniversityFeedService,
    private readonly community: CommunityFeedService,
    private readonly explore: ExploreFeedService,
  ) {}

  @Get('home')
  @ApiOperation({ summary: 'Personalized home feed' })
  async homeFeed(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: FeedQueryDto,
  ) {
    return this.home.get(user.sub, query.limit ?? 20, query.cursor);
  }

  @Get('university')
  @ApiOperation({ summary: 'University feed (members only)' })
  async universityFeed(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: UniversityFeedQueryDto,
  ) {
    return this.university.get(
      user.sub,
      query.universityId,
      query.limit ?? 20,
      query.cursor,
    );
  }

  @Get('community/:id')
  @ApiOperation({ summary: 'Community feed' })
  async communityFeed(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Query() query: FeedQueryDto,
  ) {
    return this.community.get(user.sub, id, query.limit ?? 20, query.cursor);
  }

  @Get('explore')
  @ApiOperation({ summary: 'Cross-university public discovery' })
  async exploreFeed(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: FeedQueryDto,
  ) {
    return this.explore.get(user.sub, query.limit ?? 20, query.cursor);
  }
}

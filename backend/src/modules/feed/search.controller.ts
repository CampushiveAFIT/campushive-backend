import { Controller, Get, Query, UseGuards } from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { SearchService } from './services/search.service';
import { SearchQueryDto } from './dto/feed-query.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';

@ApiTags('search')
@ApiBearerAuth('access-token')
@Controller('search')
@UseGuards(JwtAccessGuard)
export class SearchController {
  constructor(private readonly searchService: SearchService) {}

  @Get()
  @ApiOperation({ summary: 'Search users, universities, communities, events, posts' })
  async search(
    @CurrentUser() user: AccessTokenPayload,
    @Query() query: SearchQueryDto,
  ) {
    return this.searchService.search(user.sub, query.q, query.type, query.limit);
  }
}

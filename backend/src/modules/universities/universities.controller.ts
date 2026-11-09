import { Controller, Get, Param, Query, UseGuards } from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { UniversitiesService } from './universities.service';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { parsePage } from '../../common/pagination/cursor';

@ApiTags('universities')
@ApiBearerAuth('access-token')
@Controller('universities')
@UseGuards(JwtAccessGuard)
export class UniversitiesController {
  constructor(private readonly universities: UniversitiesService) {}

  @Get()
  @ApiOperation({ summary: 'List active universities' })
  async list(@Query() query: { limit?: unknown; cursor?: unknown }) {
    return this.universities.list(parsePage(query));
  }

  @Get(':slug')
  @ApiOperation({ summary: 'University detail by slug' })
  async detail(@Param('slug') slug: string) {
    return this.universities.getBySlug(slug);
  }
}

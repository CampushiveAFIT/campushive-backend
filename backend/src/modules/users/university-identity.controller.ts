import {
  Body,
  Controller,
  Get,
  HttpCode,
  HttpStatus,
  Patch,
  Post,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { UniversitiesService } from '../universities/universities.service';
import { JoinUniversityDto } from '../universities/dto/join-university.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';

@ApiTags('users')
@ApiBearerAuth('access-token')
@Controller('users')
@UseGuards(JwtAccessGuard)
export class UniversityIdentityController {
  constructor(private readonly universities: UniversitiesService) {}

  @Get('me/university')
  @ApiOperation({ summary: 'Current active university membership' })
  async myUniversity(@CurrentUser() user: AccessTokenPayload) {
    return this.universities.myUniversity(user.sub);
  }

  @Post('me/university')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Join a university (idempotent, switches primary)' })
  async join(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: JoinUniversityDto,
  ) {
    return this.universities.joinOrSwitch(user.sub, dto.universityId);
  }

  @Patch('me/university')
  @ApiOperation({ summary: 'Switch primary university' })
  async switch(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: JoinUniversityDto,
  ) {
    return this.universities.joinOrSwitch(user.sub, dto.universityId);
  }
}

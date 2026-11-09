import { Body, Controller, Get, Patch, UseGuards } from '@nestjs/common';
import {
  ApiBearerAuth,
  ApiOperation,
  ApiResponse,
  ApiTags,
} from '@nestjs/swagger';
import { UsersService } from './users.service';
import { UpdateProfileDto } from './dto/update-profile.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';

@ApiTags('users')
@ApiBearerAuth('access-token')
@Controller('users')
@UseGuards(JwtAccessGuard)
export class UsersController {
  constructor(private readonly users: UsersService) {}

  @Get('me')
  @ApiOperation({ summary: 'Get the authenticated user profile' })
  @ApiResponse({ status: 200, description: 'Current user profile.' })
  @ApiResponse({ status: 401, description: 'Missing or invalid access token.' })
  async me(@CurrentUser() user: AccessTokenPayload) {
    return this.users.me(user.sub);
  }

  @Patch('me')
  @ApiOperation({ summary: 'Update displayName, bio, or avatarUrl' })
  @ApiResponse({ status: 200, description: 'Updated profile.' })
  async updateMe(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: UpdateProfileDto,
  ) {
    return this.users.updateProfile(user.sub, dto);
  }
}

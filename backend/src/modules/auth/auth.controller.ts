import {
  Body,
  Controller,
  HttpCode,
  HttpStatus,
  Post,
  Req,
  UseGuards,
} from '@nestjs/common';
import {
  ApiBearerAuth,
  ApiOperation,
  ApiResponse,
  ApiTags,
} from '@nestjs/swagger';
import type { Request } from 'express';
import { AuthService } from './auth.service';
import { RegisterDto } from './dto/register.dto';
import { LoginDto } from './dto/login.dto';
import { RefreshTokenDto } from './dto/refresh-token.dto';
import { PasswordChangeDto } from './dto/password-change.dto';
import {
  PasswordResetConfirmDto,
  PasswordResetRequestDto,
} from './dto/password-reset.dto';
import {
  EmailVerifyConfirmDto,
  EmailVerifyRequestDto,
} from './dto/email-verify.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';
import { RateLimit } from '../../common/ratelimit/rate-limit.decorator';
import { RateLimitGuard } from '../../common/ratelimit/rate-limit.guard';

@ApiTags('auth')
@Controller('auth')
@UseGuards(RateLimitGuard)
export class AuthController {
  constructor(private readonly auth: AuthService) {}

  @Post('register')
  @RateLimit({ limit: 5, windowSec: 900, key: 'register' })
  @ApiOperation({ summary: 'Register a new account (does not authenticate)' })
  @ApiResponse({ status: 201, description: 'Account created; verification email sent.' })
  @ApiResponse({ status: 409, description: 'Email already registered.' })
  async register(@Body() dto: RegisterDto) {
    return this.auth.register(dto);
  }

  @Post('login')
  @RateLimit({ limit: 5, windowSec: 900, key: 'login' })
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Authenticate and receive access + refresh tokens' })
  @ApiResponse({ status: 200, description: 'Authenticated.' })
  @ApiResponse({ status: 401, description: 'Invalid credentials.' })
  async login(@Body() dto: LoginDto) {
    return this.auth.login(dto);
  }

  @Post('refresh')
  @RateLimit({ limit: 10, windowSec: 900, key: 'refresh' })
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Rotate refresh token and receive new credentials' })
  async refresh(@Body() dto: RefreshTokenDto) {
    return this.auth.refresh(dto.refreshToken);
  }

  @Post('logout')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Revoke the current refresh-token session' })
  async logout(@Body() dto: RefreshTokenDto, @Req() _req: Request) {
    return this.auth.logout(dto.refreshToken);
  }

  @Post('password/change')
  @UseGuards(JwtAccessGuard)
  @HttpCode(HttpStatus.OK)
  @ApiBearerAuth('access-token')
  @ApiOperation({ summary: 'Change password (invalidates all sessions)' })
  async changePassword(
    @CurrentUser() user: AccessTokenPayload,
    @Body() dto: PasswordChangeDto,
  ) {
    await this.auth.changePassword(user.sub, dto);
    return { changed: true };
  }

  @Post('password/reset/request')
  @RateLimit({ limit: 3, windowSec: 900, key: 'password-reset' })
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Request a password reset email (generic response)' })
  async requestPasswordReset(@Body() dto: PasswordResetRequestDto) {
    await this.auth.requestPasswordReset(dto);
    return { requested: true };
  }

  @Post('password/reset/confirm')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Reset password with a valid reset token' })
  async confirmPasswordReset(@Body() dto: PasswordResetConfirmDto) {
    await this.auth.confirmPasswordReset(dto);
    return { reset: true };
  }

  @Post('email/verify/request')
  @RateLimit({ limit: 3, windowSec: 900, key: 'email-verify' })
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Request an email verification message' })
  async requestEmailVerification(@Body() dto: EmailVerifyRequestDto) {
    await this.auth.requestEmailVerification(dto);
    return { requested: true };
  }

  @Post('email/verify/confirm')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Confirm email verification with a valid token' })
  async confirmEmailVerification(@Body() dto: EmailVerifyConfirmDto) {
    return this.auth.confirmEmailVerification(dto);
  }
}

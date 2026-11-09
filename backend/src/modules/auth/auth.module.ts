import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PrismaModule } from '../../config/prisma.module';
import { RedisModule } from '../../common/redis/redis.module';
import { EmailService } from '../../common/email/email.service';
import { RateLimitService } from '../../common/ratelimit/rate-limit.service';
import { RateLimitGuard } from '../../common/ratelimit/rate-limit.guard';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { RolesGuard } from '../../common/guards/roles.guard';
import { AuthController } from './auth.controller';
import { AuthService } from './auth.service';
import { PasswordService } from './password.service';
import { TokensService } from './tokens.service';

@Module({
  imports: [PrismaModule, RedisModule, JwtModule.register({})],
  controllers: [AuthController],
  providers: [
    AuthService,
    PasswordService,
    TokensService,
    EmailService,
    RateLimitService,
    RateLimitGuard,
    JwtAccessGuard,
    RolesGuard,
  ],
  exports: [AuthService, JwtAccessGuard, RolesGuard],
})
export class AuthModule {}

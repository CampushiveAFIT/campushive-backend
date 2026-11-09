import { createParamDecorator, ExecutionContext } from '@nestjs/common';
import type { AccessTokenPayload } from '../guards/jwt-access.guard';

/** Extracts the validated access-token payload attached by JwtAccessGuard. */
export const CurrentUser = createParamDecorator(
  (_data: unknown, ctx: ExecutionContext): AccessTokenPayload => {
    const req = ctx.switchToHttp().getRequest<{ user: AccessTokenPayload }>();
    return req.user;
  },
);

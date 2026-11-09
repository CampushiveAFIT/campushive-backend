import {
  CanActivate,
  ExecutionContext,
  ForbiddenException,
  Injectable,
} from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import type { Role } from '@prisma/client';
import { ROLES_KEY } from '../decorators/roles.decorator';
import type { AccessTokenPayload } from './jwt-access.guard';
import { ApiErrorCode } from '../types/api-response';

/**
 * Enforces @Roles metadata against req.user.role (set by JwtAccessGuard).
 * Roles come from the signed token / database, never from client input.
 * No-ops on routes without @Roles metadata.
 */
@Injectable()
export class RolesGuard implements CanActivate {
  constructor(private readonly reflector: Reflector) {}

  canActivate(context: ExecutionContext): boolean {
    const required = this.reflector.getAllAndOverride<Role[]>(ROLES_KEY, [
      context.getHandler(),
      context.getClass(),
    ]);
    if (!required || required.length === 0) return true;

    const req = context
      .switchToHttp()
      .getRequest<{ user?: AccessTokenPayload }>();
    const role = req.user?.role as Role | undefined;
    if (!role || !required.includes(role)) {
      throw new ForbiddenException({
        code: ApiErrorCode.FORBIDDEN,
        message: 'You do not have permission to perform this action.',
      });
    }
    return true;
  }
}

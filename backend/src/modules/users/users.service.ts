import { Injectable, NotFoundException } from '@nestjs/common';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import type { SafeUser } from '../auth/auth.service';
import { UpdateProfileDto } from './dto/update-profile.dto';

@Injectable()
export class UsersService {
  constructor(private readonly prisma: PrismaService) {}

  private toSafe(user: {
    id: string;
    email: string;
    displayName: string | null;
    avatarUrl: string | null;
    bio: string | null;
    role: SafeUser['role'];
    verificationStatus: SafeUser['verificationStatus'];
    emailVerifiedAt: Date | null;
    createdAt: Date;
    updatedAt: Date;
  }): SafeUser {
    // Explicit field list: passwordHash must never leave the service layer.
    return {
      id: user.id,
      email: user.email,
      displayName: user.displayName,
      avatarUrl: user.avatarUrl,
      bio: user.bio,
      role: user.role,
      verificationStatus: user.verificationStatus,
      emailVerifiedAt: user.emailVerifiedAt,
      createdAt: user.createdAt,
      updatedAt: user.updatedAt,
    };
  }

  async me(userId: string): Promise<SafeUser> {
    const user = await this.prisma.user.findUnique({ where: { id: userId } });
    if (!user || !user.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'User not found.',
      });
    }
    return this.toSafe(user);
  }

  async updateProfile(userId: string, dto: UpdateProfileDto): Promise<SafeUser> {
    // Only whitelisted fields reach Prisma. Role/verification/isActive
    // cannot be modified through this endpoint by construction.
    const data: Record<string, unknown> = {};
    if (dto.displayName !== undefined) {
      data.displayName = dto.displayName.trim() ? dto.displayName.trim() : null;
    }
    if (dto.bio !== undefined) data.bio = dto.bio;
    if (dto.avatarUrl !== undefined) data.avatarUrl = dto.avatarUrl;

    const user = await this.prisma.user.update({
      where: { id: userId },
      data,
    });
    return this.toSafe(user);
  }
}

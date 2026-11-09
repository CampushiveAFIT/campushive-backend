import {
  ConflictException,
  Injectable,
  UnauthorizedException,
  NotFoundException,
  BadRequestException,
} from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { Prisma, Role, User, VerificationStatus } from '@prisma/client';
import { PrismaService } from '../../config/prisma.service';
import { PasswordService } from './password.service';
import { TokensService } from './tokens.service';
import { EmailService } from '../../common/email/email.service';
import { sha256Hex } from '../../common/crypto/token-hash.util';
import { ApiErrorCode } from '../../common/types/api-response';
import { RegisterDto } from './dto/register.dto';
import { LoginDto } from './dto/login.dto';
import { PasswordChangeDto } from './dto/password-change.dto';
import {
  PasswordResetConfirmDto,
  PasswordResetRequestDto,
} from './dto/password-reset.dto';
import {
  EmailVerifyConfirmDto,
  EmailVerifyRequestDto,
} from './dto/email-verify.dto';

export interface SafeUser {
  id: string;
  email: string;
  displayName: string | null;
  avatarUrl: string | null;
  bio: string | null;
  role: Role;
  verificationStatus: VerificationStatus;
  emailVerifiedAt: Date | null;
  createdAt: Date;
  updatedAt: Date;
}

export interface AuthResult {
  user: SafeUser;
  accessToken: string;
  refreshToken: string;
}

const EMAIL_VERIFY_TTL_MS = 48 * 60 * 60 * 1000;
const PASSWORD_RESET_TTL_MS = 24 * 60 * 60 * 1000;

@Injectable()
export class AuthService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly passwords: PasswordService,
    private readonly tokens: TokensService,
    private readonly email: EmailService,
    private readonly config: ConfigService,
  ) {}

  toSafeUser(user: User): SafeUser {
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

  private frontendBase(): string {
    const origins = this.config.get<string[]>('corsOrigins', []);
    return origins[0] ?? 'http://localhost:5173';
  }

  // ---------------------------------------------------------------- register
  async register(dto: RegisterDto): Promise<SafeUser> {
    // Fast-path pre-check for the common case. The authoritative protection
    // against concurrent duplicates is the database unique constraint below.
    const existing = await this.prisma.user.findUnique({
      where: { email: dto.email },
      select: { id: true },
    });
    if (existing) {
      throw new ConflictException({
        code: ApiErrorCode.DUPLICATE_EMAIL,
        message: 'An account with this email already exists.',
      });
    }

    let user: User;
    try {
      user = await this.prisma.user.create({
        data: {
          email: dto.email,
          passwordHash: await this.passwords.hash(dto.password),
          displayName: dto.displayName?.trim() ? dto.displayName.trim() : null,
          role: Role.STUDENT,
          verificationStatus: VerificationStatus.UNVERIFIED,
        },
      });
    } catch (e) {
      if (
        e instanceof Prisma.PrismaClientKnownRequestError &&
        e.code === 'P2002'
      ) {
        const target = (e.meta?.target as string[] | undefined) ?? [];
        if (target.includes('email')) {
          throw new ConflictException({
            code: ApiErrorCode.DUPLICATE_EMAIL,
            message: 'An account with this email already exists.',
          });
        }
      }
      throw e;
    }

    await this.issueEmailVerification(user.id, user.email);
    // Registration does NOT authenticate: no tokens are issued here.
    return this.toSafeUser(user);
  }

  // ------------------------------------------------------------------- login
  async login(dto: LoginDto): Promise<AuthResult> {
    const user = await this.prisma.user.findUnique({
      where: { email: dto.email },
    });
    // Generic error: never reveal whether the email exists.
    if (!user || !user.isActive) {
      throw new UnauthorizedException({
        code: ApiErrorCode.INVALID_CREDENTIALS,
        message: 'Invalid credentials.',
      });
    }
    const ok = await this.passwords.verify(user.passwordHash, dto.password);
    if (!ok) {
      throw new UnauthorizedException({
        code: ApiErrorCode.INVALID_CREDENTIALS,
        message: 'Invalid credentials.',
      });
    }
    return this.issueSession(user);
  }

  // ----------------------------------------------------------------- refresh
  async refresh(rawRefreshToken: string): Promise<AuthResult> {
    const digest = this.tokens.digestRefreshToken(rawRefreshToken);
    const record = await this.prisma.refreshToken.findUnique({
      where: { tokenHash: digest },
      include: { user: true },
    });
    if (!record) {
      throw new UnauthorizedException({
        code: ApiErrorCode.REVOKED_TOKEN,
        message: 'Refresh token is no longer valid.',
      });
    }

    // Reuse detection: a revoked-but-replaced token being presented again
    // indicates possible token theft. Revoke the whole family, reject.
    if (record.revokedAt) {
      if (record.replacedByTokenId) {
        await this.prisma.refreshToken.updateMany({
          where: { familyId: record.familyId, revokedAt: null },
          data: { revokedAt: new Date() },
        });
      }
      throw new UnauthorizedException({
        code: ApiErrorCode.REVOKED_TOKEN,
        message: 'Refresh token is no longer valid.',
      });
    }

    if (record.expiresAt.getTime() <= Date.now()) {
      throw new UnauthorizedException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Refresh token has expired.',
      });
    }

    const user = record.user;
    if (!user || !user.isActive) {
      throw new UnauthorizedException({
        code: ApiErrorCode.INVALID_CREDENTIALS,
        message: 'Account is no longer active.',
      });
    }

    // Atomic claim: exactly one concurrent request can flip this token from
    // live to revoked. PostgreSQL evaluates the conditional UPDATE against
    // the row lock, so losers observe count === 0 after the winner commits.
    const rotation = await this.prisma.$transaction(async (tx) => {
      const now = new Date();
      const claimed = await tx.refreshToken.updateMany({
        where: {
          tokenHash: digest,
          revokedAt: null,
          expiresAt: { gt: now },
        },
        data: { revokedAt: now },
      });
      if (claimed.count !== 1) {
        return { won: false as const };
      }
      const next = this.tokens.newRefreshToken();
      const created = await tx.refreshToken.create({
        data: {
          userId: user.id,
          tokenHash: next.digest,
          familyId: record.familyId,
          expiresAt: next.expiresAt,
        },
      });
      await tx.refreshToken.update({
        where: { id: record.id },
        data: { replacedByTokenId: created.id },
      });
      return { won: true as const, raw: next.raw, createdId: created.id };
    });

    if (!rotation.won) {
      // Lost a concurrent race: the winner has committed a full rotation
      // (revoke + successor + link are one transaction), so this replay now
      // looks exactly like token reuse → burn the family, reject.
      // (If the token instead expired mid-flight, report expiry precisely.)
      const current = await this.prisma.refreshToken.findUnique({
        where: { tokenHash: digest },
      });
      if (!current) {
        throw new UnauthorizedException({
          code: ApiErrorCode.REVOKED_TOKEN,
          message: 'Refresh token is no longer valid.',
        });
      }
      if (current.revokedAt && current.replacedByTokenId) {
        await this.prisma.refreshToken.updateMany({
          where: { familyId: current.familyId, revokedAt: null },
          data: { revokedAt: new Date() },
        });
        throw new UnauthorizedException({
          code: ApiErrorCode.REVOKED_TOKEN,
          message: 'Refresh token is no longer valid.',
        });
      }
      if (current.revokedAt) {
        throw new UnauthorizedException({
          code: ApiErrorCode.REVOKED_TOKEN,
          message: 'Refresh token is no longer valid.',
        });
      }
      throw new UnauthorizedException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Refresh token has expired.',
      });
    }

    await this.prisma.refreshToken
      .update({
        where: { id: rotation.createdId },
        data: { lastUsedAt: new Date() },
      })
      .catch(() => undefined);

    return {
      user: this.toSafeUser(user),
      accessToken: this.tokens.signAccessToken(user),
      refreshToken: rotation.raw,
    };
  }

  // ------------------------------------------------------------------ logout
  /** Revokes only the presented refresh-token session. */
  async logout(rawRefreshToken: string): Promise<{ revoked: boolean }> {
    const digest = this.tokens.digestRefreshToken(rawRefreshToken);
    const res = await this.prisma.refreshToken.updateMany({
      where: { tokenHash: digest, revokedAt: null },
      data: { revokedAt: new Date() },
    });
    return { revoked: res.count > 0 };
  }

  // ---------------------------------------------------------- password change
  async changePassword(userId: string, dto: PasswordChangeDto): Promise<void> {
    const user = await this.prisma.user.findUnique({ where: { id: userId } });
    if (!user || !user.isActive) {
      throw new UnauthorizedException({
        code: ApiErrorCode.INVALID_CREDENTIALS,
        message: 'Account is no longer active.',
      });
    }
    const ok = await this.passwords.verify(user.passwordHash, dto.currentPassword);
    if (!ok) {
      throw new UnauthorizedException({
        code: ApiErrorCode.INVALID_CREDENTIALS,
        message: 'Current password is incorrect.',
      });
    }
    await this.prisma.$transaction([
      this.prisma.user.update({
        where: { id: userId },
        data: { passwordHash: await this.passwords.hash(dto.newPassword) },
      }),
      // Invalidate all sessions: the user must authenticate again everywhere.
      this.prisma.refreshToken.updateMany({
        where: { userId, revokedAt: null },
        data: { revokedAt: new Date() },
      }),
    ]);
  }

  // ---------------------------------------------------------- password reset
  async requestPasswordReset(_dto: PasswordResetRequestDto): Promise<void> {
    // Generic response regardless of whether the email exists (no enumeration).
    const user = await this.prisma.user.findUnique({
      where: { email: _dto.email },
    });
    if (user && user.isActive) {
      const { raw, digest } = this.tokens.newOneTimeToken();
      await this.prisma.passwordResetToken.create({
        data: {
          userId: user.id,
          tokenHash: digest,
          expiresAt: new Date(Date.now() + PASSWORD_RESET_TTL_MS),
        },
      });
      await this.email.sendPasswordResetEmail(
        user.email,
        `${this.frontendBase()}/reset-password?token=${raw}`,
      );
    }
  }

  async confirmPasswordReset(dto: PasswordResetConfirmDto): Promise<void> {
    const digest = sha256Hex(dto.token);
    const record = await this.prisma.passwordResetToken.findUnique({
      where: { tokenHash: digest },
    });
    if (
      !record ||
      record.usedAt ||
      record.expiresAt.getTime() <= Date.now()
    ) {
      throw new BadRequestException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Password reset token is invalid or has expired.',
      });
    }
    const user = await this.prisma.user.findUnique({
      where: { id: record.userId },
    });
    if (!user || !user.isActive) {
      throw new BadRequestException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Password reset token is invalid or has expired.',
      });
    }
    await this.prisma.$transaction([
      this.prisma.user.update({
        where: { id: user.id },
        data: { passwordHash: await this.passwords.hash(dto.newPassword) },
      }),
      this.prisma.passwordResetToken.update({
        where: { id: record.id },
        data: { usedAt: new Date() },
      }),
      this.prisma.refreshToken.updateMany({
        where: { userId: user.id, revokedAt: null },
        data: { revokedAt: new Date() },
      }),
    ]);
  }

  // ------------------------------------------------------- email verification
  async requestEmailVerification(dto: EmailVerifyRequestDto): Promise<void> {
    // Generic success: do not reveal whether the email is registered.
    const user = await this.prisma.user.findUnique({
      where: { email: dto.email },
    });
    if (user && user.isActive && !user.emailVerifiedAt) {
      await this.issueEmailVerification(user.id, user.email);
    }
  }

  async confirmEmailVerification(dto: EmailVerifyConfirmDto): Promise<SafeUser> {
    const digest = sha256Hex(dto.token);
    const record = await this.prisma.emailVerificationToken.findUnique({
      where: { tokenHash: digest },
    });
    if (!record || record.usedAt || record.expiresAt.getTime() <= Date.now()) {
      throw new BadRequestException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Verification token is invalid or has expired.',
      });
    }
    const user = await this.prisma.user.findUnique({
      where: { id: record.userId },
    });
    if (!user || !user.isActive) {
      throw new BadRequestException({
        code: ApiErrorCode.EXPIRED_TOKEN,
        message: 'Verification token is invalid or has expired.',
      });
    }
    if (user.emailVerifiedAt) {
      // Already verified: mark token used, return current state (idempotent).
      await this.prisma.emailVerificationToken.update({
        where: { id: record.id },
        data: { usedAt: new Date() },
      });
      return this.toSafeUser(user);
    }
    const [updated] = await this.prisma.$transaction([
      this.prisma.user.update({
        where: { id: user.id },
        data: {
          emailVerifiedAt: new Date(),
          verificationStatus: VerificationStatus.VERIFIED,
        },
      }),
      this.prisma.emailVerificationToken.update({
        where: { id: record.id },
        data: { usedAt: new Date() },
      }),
    ]);
    return this.toSafeUser(updated);
  }

  // ----------------------------------------------------------------- helpers
  private async issueEmailVerification(userId: string, email: string): Promise<void> {
    const { raw, digest } = this.tokens.newOneTimeToken();
    await this.prisma.emailVerificationToken.create({
      data: {
        userId,
        tokenHash: digest,
        expiresAt: new Date(Date.now() + EMAIL_VERIFY_TTL_MS),
      },
    });
    await this.email.sendVerificationEmail(
      email,
      `${this.frontendBase()}/verify-email?token=${raw}`,
    );
  }

  private async issueSession(user: User): Promise<AuthResult> {
    const accessToken = this.tokens.signAccessToken(user);
    const next = this.tokens.newRefreshToken();
    const { randomUUID } = await import('crypto');
    await this.prisma.refreshToken.create({
      data: {
        userId: user.id,
        tokenHash: next.digest,
        familyId: randomUUID(),
        expiresAt: next.expiresAt,
      },
    });
    return { user: this.toSafeUser(user), accessToken, refreshToken: next.raw };
  }

  async findActiveUserOrThrow(userId: string): Promise<User> {
    const user = await this.prisma.user.findUnique({ where: { id: userId } });
    if (!user || !user.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'User not found.',
      });
    }
    return user;
  }

  async revokeAllSessions(userId: string): Promise<void> {
    await this.prisma.refreshToken.updateMany({
      where: { userId, revokedAt: null },
      data: { revokedAt: new Date() },
    });
  }
}

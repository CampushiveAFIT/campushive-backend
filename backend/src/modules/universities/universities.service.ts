import {
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { PrismaService } from '../../config/prisma.service';
import { ApiErrorCode } from '../../common/types/api-response';
import {
  ParsedPage,
  olderThan,
  toPage,
  PageResult,
} from '../../common/pagination/cursor';

export interface UniversityView {
  id: string;
  name: string;
  slug: string;
  domain: string | null;
  description: string | null;
  logoUrl: string | null;
  isActive: boolean;
  createdAt: Date;
  updatedAt: Date;
}

export interface MembershipView {
  id: string;
  status: string;
  role: string;
  verificationStatus: string;
  university: UniversityView;
  createdAt: Date;
  updatedAt: Date;
}

@Injectable()
export class UniversitiesService {
  constructor(private readonly prisma: PrismaService) {}

  private toView(u: {
    id: string;
    name: string;
    slug: string;
    domain: string | null;
    description: string | null;
    logoUrl: string | null;
    isActive: boolean;
    createdAt: Date;
    updatedAt: Date;
  }): UniversityView {
    return { ...u };
  }

  async list(page: ParsedPage): Promise<PageResult<UniversityView>> {
    const where = { isActive: true };
    const cursorWhere =
      page.cursor != null
        ? { AND: [where, olderThan(page.cursor.createdAt, page.cursor.id)] }
        : where;
    const rows = await this.prisma.university.findMany({
      where: cursorWhere,
      orderBy: [{ createdAt: 'desc' }, { id: 'desc' }],
      take: page.limit + 1,
    });
    const result = toPage(rows, page.limit);
    return { items: result.items.map((u) => this.toView(u)), nextCursor: result.nextCursor };
  }

  async getBySlug(slug: string): Promise<UniversityView & { memberCount: number; communityCount: number }> {
    const u = await this.prisma.university.findUnique({ where: { slug } });
    if (!u || !u.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'University not found.',
      });
    }
    const [memberCount, communityCount] = await Promise.all([
      this.prisma.universityMembership.count({
        where: { universityId: u.id, status: 'ACTIVE' },
      }),
      this.prisma.community.count({
        where: { universityId: u.id, isActive: true },
      }),
    ]);
    return { ...this.toView(u), memberCount, communityCount };
  }

  async requireActiveUniversity(universityId: string) {
    const u = await this.prisma.university.findUnique({
      where: { id: universityId },
    });
    if (!u || !u.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'University not found.',
      });
    }
    return u;
  }

  /** The user's single ACTIVE membership (product invariant: one primary). */
  async myUniversity(userId: string): Promise<MembershipView> {
    const m = await this.prisma.universityMembership.findFirst({
      where: { userId, status: 'ACTIVE' },
      include: { university: true },
      orderBy: { updatedAt: 'desc' },
    });
    if (!m || !m.university.isActive) {
      throw new NotFoundException({
        code: ApiErrorCode.NOT_FOUND,
        message: 'No active university membership.',
      });
    }
    return this.toMembershipView(m);
  }

  /**
   * Idempotent join-or-switch. Creates (or reactivates) an ACTIVE membership
   * and suspends any other ACTIVE memberships so the one-primary invariant
   * holds. Never accepts role/status/verification from the client.
   */
  async joinOrSwitch(userId: string, universityId: string): Promise<MembershipView> {
    await this.requireActiveUniversity(universityId);
    const existing = await this.prisma.universityMembership.findUnique({
      where: { userId_universityId: { userId, universityId } },
      include: { university: true },
    });
    if (existing && existing.status === 'ACTIVE') {
      return this.toMembershipView(existing);
    }
    const membership = await this.prisma.$transaction(async (tx) => {
      await tx.universityMembership.updateMany({
        where: {
          userId,
          status: 'ACTIVE',
          universityId: { not: universityId },
        },
        data: { status: 'SUSPENDED' },
      });
      if (existing) {
        return tx.universityMembership.update({
          where: { id: existing.id },
          data: { status: 'ACTIVE' },
          include: { university: true },
        });
      }
      return tx.universityMembership.create({
        data: {
          userId,
          universityId,
          status: 'ACTIVE',
          role: 'STUDENT',
          verificationStatus: 'UNVERIFIED',
        },
        include: { university: true },
      });
    });
    return this.toMembershipView(membership);
  }

  private toMembershipView(m: {
    id: string;
    status: string;
    role: string;
    verificationStatus: string;
    createdAt: Date;
    updatedAt: Date;
    university: {
      id: string;
      name: string;
      slug: string;
      domain: string | null;
      description: string | null;
      logoUrl: string | null;
      isActive: boolean;
      createdAt: Date;
      updatedAt: Date;
    };
  }): MembershipView {
    return {
      id: m.id,
      status: m.status,
      role: m.role,
      verificationStatus: m.verificationStatus,
      university: this.toView(m.university),
      createdAt: m.createdAt,
      updatedAt: m.updatedAt,
    };
  }
}

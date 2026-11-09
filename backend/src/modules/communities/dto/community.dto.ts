import {
  IsEnum,
  IsOptional,
  IsString,
  IsUUID,
  Matches,
  MaxLength,
  MinLength,
} from 'class-validator';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';
import { CommunityType, CommunityVisibility } from '@prisma/client';

export class CreateCommunityDto {
  @ApiProperty()
  @IsUUID('4')
  universityId!: string;

  @ApiProperty({ example: 'Computer Science Club' })
  @IsString()
  @MinLength(1)
  @MaxLength(120)
  name!: string;

  @ApiProperty({ example: 'cs-club' })
  @IsString()
  @MinLength(1)
  @MaxLength(80)
  @Matches(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, {
    message: 'slug must be lowercase alphanumeric with single hyphens.',
  })
  slug!: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @MaxLength(2000)
  description?: string;

  @ApiPropertyOptional({ enum: CommunityType })
  @IsOptional()
  @IsEnum(CommunityType)
  type?: CommunityType;

  @ApiPropertyOptional({ enum: CommunityVisibility })
  @IsOptional()
  @IsEnum(CommunityVisibility)
  visibility?: CommunityVisibility;
}

export class UpdateCommunityDto {
  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @MinLength(1)
  @MaxLength(120)
  name?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @MinLength(1)
  @MaxLength(80)
  @Matches(/^[a-z0-9]+(?:-[a-z0-9]+)*$/, {
    message: 'slug must be lowercase alphanumeric with single hyphens.',
  })
  slug?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @MaxLength(2000)
  description?: string;

  @ApiPropertyOptional({ enum: CommunityType })
  @IsOptional()
  @IsEnum(CommunityType)
  type?: CommunityType;

  @ApiPropertyOptional({ enum: CommunityVisibility })
  @IsOptional()
  @IsEnum(CommunityVisibility)
  visibility?: CommunityVisibility;
}

export class UpdateMemberRoleDto {
  @ApiProperty({ enum: ['MEMBER', 'MODERATOR'] })
  @IsEnum(['MEMBER', 'MODERATOR'] as const, {
    message: 'role must be MEMBER or MODERATOR.',
  })
  role!: 'MEMBER' | 'MODERATOR';
}

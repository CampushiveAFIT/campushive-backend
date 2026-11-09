import {
  IsEnum,
  IsOptional,
  IsString,
  IsUUID,
  MaxLength,
  MinLength,
} from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';
import { PostVisibility } from '@prisma/client';

export class CreatePostDto {
  @ApiProperty({ example: 'Physics study group meets Friday in Hall B.' })
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'content must not be empty.' })
  @MaxLength(5000)
  content!: string;

  @ApiPropertyOptional({ enum: PostVisibility, default: PostVisibility.UNIVERSITY })
  @IsOptional()
  @IsEnum(PostVisibility)
  visibility?: PostVisibility;

  @ApiPropertyOptional({ description: 'Community to post into' })
  @IsOptional()
  @IsUUID('4')
  communityId?: string;

  @ApiPropertyOptional({ description: 'Existing event to reference (no data is copied)' })
  @IsOptional()
  @IsUUID('4')
  eventId?: string;
}

export class UpdatePostDto {
  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'content must not be empty.' })
  @MaxLength(5000)
  content?: string;

  @ApiPropertyOptional({ enum: PostVisibility })
  @IsOptional()
  @IsEnum(PostVisibility)
  visibility?: PostVisibility;
}

export class CreateCommentDto {
  @ApiProperty({ example: 'Count me in!' })
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'content must not be empty.' })
  @MaxLength(2000)
  content!: string;
}

export class UpdateCommentDto {
  @ApiProperty()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'content must not be empty.' })
  @MaxLength(2000)
  content!: string;
}

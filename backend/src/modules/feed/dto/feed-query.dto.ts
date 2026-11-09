import { IsInt, IsOptional, IsString, Max, MaxLength, Min } from 'class-validator';
import { Type } from 'class-transformer';
import { ApiPropertyOptional } from '@nestjs/swagger';

export class FeedQueryDto {
  @ApiPropertyOptional({ default: 20 })
  @IsOptional()
  @Type(() => Number)
  @IsInt()
  @Min(1)
  @Max(50)
  limit?: number;

  @ApiPropertyOptional({ description: 'Opaque cursor from a previous page' })
  @IsOptional()
  @IsString()
  @MaxLength(512)
  cursor?: string;
}

export class UniversityFeedQueryDto extends FeedQueryDto {
  @ApiPropertyOptional({ description: 'Defaults to your primary university' })
  @IsOptional()
  @IsString()
  @MaxLength(36)
  universityId?: string;
}

export enum SearchType {
  USERS = 'users',
  UNIVERSITIES = 'universities',
  COMMUNITIES = 'communities',
  EVENTS = 'events',
  POSTS = 'posts',
}

export class SearchQueryDto {
  @ApiPropertyOptional({ example: 'robotics' })
  @IsString()
  @MaxLength(120)
  q!: string;

  @ApiPropertyOptional({ enum: SearchType })
  @IsOptional()
  @IsString()
  type?: string;

  @ApiPropertyOptional({ default: 10 })
  @IsOptional()
  @Type(() => Number)
  @IsInt()
  @Min(1)
  @Max(25)
  limit?: number;
}

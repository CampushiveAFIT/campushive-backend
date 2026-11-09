import { IsOptional, IsString, IsUrl, MaxLength } from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiPropertyOptional } from '@nestjs/swagger';

/**
 * Only explicit profile fields are accepted. Role, verification status,
 * activity flags, and password hashes can never be modified here.
 */
export class UpdateProfileDto {
  @ApiPropertyOptional({ example: 'Adaeze Okafor' })
  @IsOptional()
  @IsString()
  @MaxLength(120)
  @Transform(({ value }) =>
    typeof value === 'string' ? value.trim() : value,
  )
  displayName?: string;

  @ApiPropertyOptional({ example: 'Final-year CS student.' })
  @IsOptional()
  @IsString()
  @MaxLength(500)
  bio?: string;

  @ApiPropertyOptional({ example: 'https://cdn.example.com/avatars/a.png' })
  @IsOptional()
  @IsUrl({ require_protocol: true })
  @MaxLength(2048)
  avatarUrl?: string;
}

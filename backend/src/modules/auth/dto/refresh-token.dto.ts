import { IsString, MaxLength, MinLength } from 'class-validator';
import { ApiProperty } from '@nestjs/swagger';

export class RefreshTokenDto {
  @ApiProperty({ description: 'Opaque refresh token issued at login/refresh' })
  @IsString()
  @MinLength(32)
  @MaxLength(256)
  refreshToken!: string;
}

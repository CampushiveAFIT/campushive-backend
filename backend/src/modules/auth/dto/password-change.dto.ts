import { IsString, Matches, MaxLength, MinLength } from 'class-validator';
import { ApiProperty } from '@nestjs/swagger';
import { PASSWORD_PATTERN } from './register.dto';

export class PasswordChangeDto {
  @ApiProperty()
  @IsString()
  @MaxLength(128)
  currentPassword!: string;

  @ApiProperty({ example: 'N3w-Str0ng-Pass!' })
  @IsString()
  @MinLength(8, { message: 'newPassword must be at least 8 characters' })
  @MaxLength(128)
  @Matches(PASSWORD_PATTERN, {
    message:
      'newPassword must contain at least one lowercase letter, one uppercase letter, and one digit',
  })
  newPassword!: string;
}

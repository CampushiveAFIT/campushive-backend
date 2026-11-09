import { IsEmail, IsString, Matches, MaxLength, MinLength } from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiProperty } from '@nestjs/swagger';
import { PASSWORD_PATTERN } from './register.dto';

export class PasswordResetRequestDto {
  @ApiProperty({ example: 'adaeze@example.com' })
  @IsEmail()
  @MaxLength(255)
  @Transform(({ value }) =>
    typeof value === 'string' ? value.toLowerCase().trim() : value,
  )
  email!: string;
}

export class PasswordResetConfirmDto {
  @ApiProperty({ description: 'Raw reset token from the email link' })
  @IsString()
  @MinLength(32)
  @MaxLength(256)
  token!: string;

  @ApiProperty({ example: 'An0ther-Str0ng-Pass!' })
  @IsString()
  @MinLength(8, { message: 'newPassword must be at least 8 characters' })
  @MaxLength(128)
  @Matches(PASSWORD_PATTERN, {
    message:
      'newPassword must contain at least one lowercase letter, one uppercase letter, and one digit',
  })
  newPassword!: string;
}

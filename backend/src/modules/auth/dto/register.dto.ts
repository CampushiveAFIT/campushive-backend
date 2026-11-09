import { IsEmail, IsOptional, IsString, Matches, MaxLength, MinLength } from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiProperty } from '@nestjs/swagger';

export const PASSWORD_PATTERN =
  /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d).{8,}$/;

export class RegisterDto {
  @ApiProperty({ example: 'adaeze@example.com' })
  @IsEmail({}, { message: 'email must be a valid email address' })
  @MaxLength(255)
  @Transform(({ value }) =>
    typeof value === 'string' ? value.toLowerCase().trim() : value,
  )
  email!: string;

  @ApiProperty({ example: 'Str0ng-Passw0rd!' })
  @IsString()
  @MinLength(8, { message: 'password must be at least 8 characters' })
  @MaxLength(128)
  @Matches(PASSWORD_PATTERN, {
    message:
      'password must contain at least one lowercase letter, one uppercase letter, and one digit',
  })
  password!: string;

  @ApiProperty({ example: 'Adaeze Okafor', required: false })
  @IsOptional()
  @IsString()
  @MaxLength(120)
  @Transform(({ value }) =>
    typeof value === 'string' ? value.trim() : value,
  )
  displayName?: string;
}

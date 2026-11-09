import { IsEmail, IsString, MaxLength, MinLength } from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiProperty } from '@nestjs/swagger';

export class EmailVerifyRequestDto {
  @ApiProperty({ example: 'adaeze@example.com' })
  @IsEmail()
  @MaxLength(255)
  @Transform(({ value }) =>
    typeof value === 'string' ? value.toLowerCase().trim() : value,
  )
  email!: string;
}

export class EmailVerifyConfirmDto {
  @ApiProperty({ description: 'Raw verification token from the email link' })
  @IsString()
  @MinLength(32)
  @MaxLength(256)
  token!: string;
}

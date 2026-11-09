import {
  IsString,
  IsUUID,
  MaxLength,
  MinLength,
} from 'class-validator';
import { Transform } from 'class-transformer';
import { ApiProperty } from '@nestjs/swagger';

export class CreateDirectConversationDto {
  @ApiProperty({ description: 'The other user' })
  @IsUUID('4', { message: 'userId must be a valid UUID.' })
  userId!: string;
}

export class SendMessageDto {
  @ApiProperty({ example: 'Are you coming to the study session?' })
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'body must not be empty.' })
  @MaxLength(4000, { message: 'body must not exceed 4000 characters.' })
  body!: string;

  @ApiProperty({ description: 'Client idempotency key (UUID recommended)' })
  @IsString()
  @MinLength(1)
  @MaxLength(128)
  clientMessageId!: string;
}

export class UpdateMessageDto {
  @ApiProperty({ example: 'Edited: see you at 5pm instead.' })
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'body must not be empty.' })
  @MaxLength(4000, { message: 'body must not exceed 4000 characters.' })
  body!: string;
}

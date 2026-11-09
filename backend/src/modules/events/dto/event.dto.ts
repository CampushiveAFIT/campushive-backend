import {
  IsDateString,
  IsEnum,
  IsInt,
  IsOptional,
  IsString,
  IsUUID,
  Max,
  MaxLength,
  Min,
  MinLength,
} from 'class-validator';
import { Transform, Type } from 'class-transformer';
import { ApiProperty, ApiPropertyOptional } from '@nestjs/swagger';
import { EventStatus, EventType, EventVisibility } from '@prisma/client';

export class CreateEventDto {
  @ApiProperty({ example: 'Robotics Night' })
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'title must not be empty.' })
  @MaxLength(200)
  title!: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MaxLength(5000)
  description?: string;

  @ApiPropertyOptional({ enum: EventType, default: EventType.OTHER })
  @IsOptional()
  @IsEnum(EventType)
  type?: EventType;

  @ApiPropertyOptional({ example: 'Main Auditorium, Block B' })
  @IsOptional()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MaxLength(500)
  location?: string;

  @ApiProperty({ example: '2026-10-01T18:00:00.000Z' })
  @IsDateString({}, { message: 'startsAt must be a valid ISO date.' })
  startsAt!: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsDateString({}, { message: 'endsAt must be a valid ISO date.' })
  endsAt?: string;

  @ApiProperty({ example: 100 })
  @Type(() => Number)
  @IsInt()
  @Min(1, { message: 'capacity must be at least 1.' })
  @Max(100000, { message: 'capacity must not exceed 100000.' })
  capacity!: number;

  @ApiPropertyOptional()
  @IsOptional()
  @IsDateString({}, { message: 'registrationDeadline must be a valid ISO date.' })
  registrationDeadline?: string;

  @ApiPropertyOptional({ enum: EventVisibility, default: EventVisibility.UNIVERSITY })
  @IsOptional()
  @IsEnum(EventVisibility)
  visibility?: EventVisibility;

  @ApiPropertyOptional({ description: 'Community to attach the event to' })
  @IsOptional()
  @IsUUID('4')
  communityId?: string;
}

export class UpdateEventDto {
  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MinLength(1, { message: 'title must not be empty.' })
  @MaxLength(200)
  title?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MaxLength(5000)
  description?: string;

  @ApiPropertyOptional({ enum: EventType })
  @IsOptional()
  @IsEnum(EventType)
  type?: EventType;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @MaxLength(500)
  location?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsDateString({}, { message: 'startsAt must be a valid ISO date.' })
  startsAt?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsDateString({}, { message: 'endsAt must be a valid ISO date.' })
  endsAt?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @Type(() => Number)
  @IsInt()
  @Min(1, { message: 'capacity must be at least 1.' })
  @Max(100000, { message: 'capacity must not exceed 100000.' })
  capacity?: number;

  @ApiPropertyOptional()
  @IsOptional()
  @IsDateString({}, { message: 'registrationDeadline must be a valid ISO date.' })
  registrationDeadline?: string;

  @ApiPropertyOptional({ enum: EventVisibility })
  @IsOptional()
  @IsEnum(EventVisibility)
  visibility?: EventVisibility;

  @ApiPropertyOptional({ description: 'Set null to detach the community' })
  @IsOptional()
  @IsUUID('4')
  communityId?: string | null;

  @ApiPropertyOptional({ enum: EventStatus })
  @IsOptional()
  @IsEnum(EventStatus)
  status?: EventStatus;
}

export class VerifyTicketDto {
  @ApiProperty({ description: 'Opaque ticket code from the QR credential' })
  @IsString()
  @MinLength(32)
  @MaxLength(256)
  ticketCode!: string;
}

export class EventListQuery {
  @ApiPropertyOptional()
  @IsOptional()
  @Type(() => Number)
  @IsInt()
  @Min(1)
  @Max(100)
  limit?: number;

  @ApiPropertyOptional()
  @IsOptional()
  @IsString()
  @MaxLength(512)
  cursor?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsUUID('4')
  universityId?: string;

  @ApiPropertyOptional()
  @IsOptional()
  @IsUUID('4')
  communityId?: string;

  @ApiPropertyOptional({ enum: EventStatus })
  @IsOptional()
  @IsEnum(EventStatus)
  status?: EventStatus;

  @ApiPropertyOptional({ description: 'Only events starting in the future' })
  @IsOptional()
  @Transform(({ value }) => value === true || value === 'true')
  upcoming?: boolean;

  @ApiPropertyOptional({ description: 'Only events organized by me' })
  @IsOptional()
  @Transform(({ value }) => value === true || value === 'true')
  mine?: boolean;
}

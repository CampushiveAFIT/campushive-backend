import { IsUUID } from 'class-validator';
import { ApiProperty } from '@nestjs/swagger';

export class JoinUniversityDto {
  @ApiProperty({ example: 'a8098c1a-f86e-11da-bd1a-00112444be1e' })
  @IsUUID('4', { message: 'universityId must be a valid UUID.' })
  universityId!: string;
}

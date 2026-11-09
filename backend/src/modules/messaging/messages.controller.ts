import {
  Body,
  Controller,
  Delete,
  HttpCode,
  HttpStatus,
  Param,
  ParseUUIDPipe,
  Patch,
  UseGuards,
} from '@nestjs/common';
import { ApiBearerAuth, ApiOperation, ApiTags } from '@nestjs/swagger';
import { MessagesService } from './messages.service';
import { UpdateMessageDto } from './dto/messaging.dto';
import { JwtAccessGuard } from '../../common/guards/jwt-access.guard';
import { CurrentUser } from '../../common/decorators/current-user.decorator';
import type { AccessTokenPayload } from '../../common/guards/jwt-access.guard';

@ApiTags('messaging')
@ApiBearerAuth('access-token')
@Controller('messages')
@UseGuards(JwtAccessGuard)
export class MessagesController {
  constructor(private readonly messages: MessagesService) {}

  @Patch(':id')
  @ApiOperation({ summary: 'Edit my message (sender only)' })
  async update(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
    @Body() dto: UpdateMessageDto,
  ) {
    return this.messages.update(user.sub, id, dto);
  }

  @Delete(':id')
  @HttpCode(HttpStatus.OK)
  @ApiOperation({ summary: 'Soft-delete my message (sender only, idempotent)' })
  async remove(
    @CurrentUser() user: AccessTokenPayload,
    @Param('id', ParseUUIDPipe) id: string,
  ) {
    return this.messages.remove(user.sub, id);
  }
}

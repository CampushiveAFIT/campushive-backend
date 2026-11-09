import { plainToInstance } from 'class-transformer';
import { validate } from 'class-validator';
import {
  CreateDirectConversationDto,
  SendMessageDto,
  UpdateMessageDto,
} from './dto/messaging.dto';

async function errorsOf<T extends object>(
  cls: new () => T,
  plain: Record<string, unknown>,
): Promise<string[]> {
  const dto = plainToInstance(cls, plain);
  const errors = await validate(dto);
  return errors.flatMap((e) => Object.values(e.constraints ?? {}));
}

describe('messaging DTO validation', () => {
  it('accepts a well-formed send', async () => {
    expect(
      await errorsOf(SendMessageDto, {
        clientMessageId: '550e8400-e29b-41d4-a716-446655440000',
        body: '  Hello, study session at 5?  ',
      }),
    ).toEqual([]);
  });

  it('trims the body but preserves meaningful internal whitespace', async () => {
    const dto = plainToInstance<SendMessageDto, Record<string, unknown>>(
      SendMessageDto,
      {
        clientMessageId: 'k',
        body: '  hello   world\nnewline  ',
      },
    );
    expect(await validate(dto)).toEqual([]);
    expect(dto.body).toBe('hello   world\nnewline');
  });

  it('rejects empty, whitespace-only, and oversized bodies', async () => {
    expect(
      await errorsOf(SendMessageDto, { clientMessageId: 'k', body: '' }),
    ).not.toEqual([]);
    expect(
      await errorsOf(SendMessageDto, { clientMessageId: 'k', body: '   \n\t  ' }),
    ).not.toEqual([]);
    expect(
      await errorsOf(SendMessageDto, { clientMessageId: 'k', body: 'x'.repeat(4001) }),
    ).not.toEqual([]);
    expect(
      await errorsOf(SendMessageDto, { clientMessageId: 'k', body: 'x'.repeat(4000) }),
    ).toEqual([]);
  });

  it('requires the client idempotency key on send', async () => {
    expect(await errorsOf(SendMessageDto, { body: 'hi' })).not.toEqual([]);
    expect(await errorsOf(SendMessageDto, { body: 'hi', clientMessageId: '' })).not.toEqual(
      [],
    );
  });

  it('validates edit bodies with the same text rules', async () => {
    expect(await errorsOf(UpdateMessageDto, { body: 'Edited.' })).toEqual([]);
    expect(await errorsOf(UpdateMessageDto, { body: '   ' })).not.toEqual([]);
    expect(
      await errorsOf(UpdateMessageDto, { body: 'x'.repeat(4001) }),
    ).not.toEqual([]);
  });

  it('requires a UUID target for direct conversations', async () => {
    expect(
      await errorsOf(CreateDirectConversationDto, {
        userId: '550e8400-e29b-41d4-a716-446655440000',
      }),
    ).toEqual([]);
    expect(
      await errorsOf(CreateDirectConversationDto, { userId: 'not-a-uuid' }),
    ).not.toEqual([]);
    expect(await errorsOf(CreateDirectConversationDto, {})).not.toEqual([]);
  });
});

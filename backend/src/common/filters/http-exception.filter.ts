import {
  ArgumentsHost,
  Catch,
  ExceptionFilter,
  HttpException,
  HttpStatus,
  Logger,
} from '@nestjs/common';
import { Request, Response } from 'express';
import { ApiErrorCode, ApiErrorBody } from '../types/api-response';

/**
 * Shapes every error into the API contract:
 *   { success: false, error: { code, message, details? } }
 * Never leaks stack traces, SQL errors, or Prisma internals to clients.
 */
@Catch()
export class HttpExceptionFilter implements ExceptionFilter {
  private readonly logger = new Logger('HttpException');

  catch(exception: unknown, host: ArgumentsHost): void {
    const ctx = host.switchToHttp();
    const res = ctx.getResponse<Response>();
    const req = ctx.getRequest<Request>();

    let status = HttpStatus.INTERNAL_SERVER_ERROR;
    let code: string = ApiErrorCode.INTERNAL_ERROR;
    let message = 'An unexpected error occurred.';
    let details: unknown;

    // Body-parser limit errors (e.g. entity.too.large) are not HttpExceptions.
    const raw = exception as { status?: unknown; type?: unknown };
    if (raw && (raw.status === 413 || raw.type === 'entity.too.large')) {
      status = HttpStatus.PAYLOAD_TOO_LARGE;
      code = ApiErrorCode.PAYLOAD_TOO_LARGE;
      message = 'Request body too large.';
    } else if (exception instanceof HttpException) {
      status = exception.getStatus();
      const body = exception.getResponse();

      if (typeof body === 'object' && body !== null) {
        const b = body as Record<string, unknown>;
        // class-validator ValidationPipe errors: { message: string[], error, statusCode }
        if (Array.isArray(b.message)) {
          code = ApiErrorCode.VALIDATION_ERROR;
          message = 'Validation failed.';
          details = b.message;
        } else if (typeof b.code === 'string') {
          code = b.code;
          message = typeof b.message === 'string' ? b.message : message;
          if (b.details !== undefined) details = b.details;
        } else if (typeof b.message === 'string') {
          message = b.message;
          code = this.codeForStatus(status);
        }
      } else if (typeof body === 'string') {
        message = body;
        code = this.codeForStatus(status);
      }
    }

    // Log full details server-side only (never sent to client).
    if (status >= 500) {
      this.logger.error(
        `${req.method} ${req.url} -> ${status} ${code}`,
        exception instanceof Error ? exception.stack : String(exception),
      );
    }

    const payload: ApiErrorBody = {
      success: false,
      error: { code, message },
    };
    if (details !== undefined) payload.error.details = details;
    res.status(status).json(payload);
  }

  private codeForStatus(status: number): string {
    switch (status) {
      case HttpStatus.BAD_REQUEST:
        return ApiErrorCode.VALIDATION_ERROR;
      case HttpStatus.UNAUTHORIZED:
        return ApiErrorCode.INVALID_CREDENTIALS;
      case HttpStatus.FORBIDDEN:
        return ApiErrorCode.FORBIDDEN;
      case HttpStatus.NOT_FOUND:
        return ApiErrorCode.NOT_FOUND;
      case HttpStatus.CONFLICT:
        return ApiErrorCode.DUPLICATE_EMAIL;
      case HttpStatus.TOO_MANY_REQUESTS:
        return ApiErrorCode.RATE_LIMITED;
      default:
        return ApiErrorCode.INTERNAL_ERROR;
    }
  }
}

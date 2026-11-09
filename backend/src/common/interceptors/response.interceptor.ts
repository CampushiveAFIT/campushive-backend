import {
  CallHandler,
  ExecutionContext,
  Injectable,
  NestInterceptor,
} from '@nestjs/common';
import { Observable } from 'rxjs';
import { map } from 'rxjs/operators';
import { ApiSuccessBody } from '../types/api-response';

/**
 * Wraps successful controller results in the API contract:
 *   { success: true, data: <result> }
 * Controllers return plain resource objects; the envelope is applied here.
 */
@Injectable()
export class ResponseInterceptor<T>
  implements NestInterceptor<T, ApiSuccessBody<T>>
{
  intercept(
    _context: ExecutionContext,
    next: CallHandler,
  ): Observable<ApiSuccessBody<T>> {
    return next.handle().pipe(map((data) => ({ success: true, data })));
  }
}

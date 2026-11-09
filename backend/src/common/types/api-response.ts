export enum ApiErrorCode {
  VALIDATION_ERROR = 'VALIDATION_ERROR',
  INVALID_CREDENTIALS = 'INVALID_CREDENTIALS',
  DUPLICATE_EMAIL = 'DUPLICATE_EMAIL',
  EXPIRED_TOKEN = 'EXPIRED_TOKEN',
  REVOKED_TOKEN = 'REVOKED_TOKEN',
  WEAK_PASSWORD = 'WEAK_PASSWORD',
  NOT_FOUND = 'NOT_FOUND',
  FORBIDDEN = 'FORBIDDEN',
  RATE_LIMITED = 'RATE_LIMITED',
  CONFLICT = 'CONFLICT',
  PAYLOAD_TOO_LARGE = 'PAYLOAD_TOO_LARGE',
  EVENT_FULL = 'EVENT_FULL',
  REGISTRATION_CLOSED = 'REGISTRATION_CLOSED',
  INTERNAL_ERROR = 'INTERNAL_ERROR',
}

export interface ApiErrorBody {
  success: false;
  error: {
    code: ApiErrorCode | string;
    message: string;
    details?: unknown;
  };
}

export interface ApiSuccessBody<T> {
  success: true;
  data: T;
}

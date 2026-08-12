import { createHash, randomBytes } from 'node:crypto';

export class DomainCommandError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status = 400) {
    super(message);
    this.name = 'DomainCommandError';
    this.code = code;
    this.status = status;
  }
}

export function assertIdempotencyKey(value: unknown): asserts value is string {
  if (typeof value !== 'string' || value.length < 8 || value.length > 128 || !/^[A-Za-z0-9._:-]+$/.test(value)) {
    throw new DomainCommandError('INVALID_IDEMPOTENCY_KEY', 'A valid Idempotency-Key header is required.');
  }
}

export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, item]) => item !== undefined)
    .sort(([left], [right]) => left.localeCompare(right));
  return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${canonicalize(item)}`).join(',')}}`;
}

export function bodyHash(value: unknown): string {
  return createHash('sha256').update(canonicalize(value)).digest('hex');
}

export function deterministicId(prefix: string, ...parts: string[]): string {
  return `${prefix}_${bodyHash(parts.join('\u001f')).slice(0, 32)}`;
}

export function uuidV7(now = Date.now()): string {
  if (!Number.isSafeInteger(now) || now < 0 || now > 0xffffffffffff) throw new DomainCommandError('INVALID_SERVER_TIME', 'Server time is outside the UUIDv7 range.', 500);
  const bytes = randomBytes(16);
  let timestamp = BigInt(now);
  for (let index = 5; index >= 0; index -= 1) {
    bytes[index] = Number(timestamp & 0xffn);
    timestamp >>= 8n;
  }
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = bytes.toString('hex');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export function safeText(value: unknown, field: string, maxLength: number): string {
  if (typeof value !== 'string' || value.trim().length === 0 || value.length > maxLength) {
    throw new DomainCommandError(`INVALID_${field.toUpperCase()}`, `${field} is required and must be at most ${maxLength} characters.`);
  }
  return value.trim();
}

export function safeId(value: unknown, field: string): string {
  const result = safeText(value, field, 128);
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(result)) {
    throw new DomainCommandError(`INVALID_${field.toUpperCase()}`, `${field} contains unsupported characters.`);
  }
  return result;
}

export function positiveKrw(value: unknown, field = 'amountKrw'): number {
  if (!Number.isSafeInteger(value) || (value as number) <= 0 || (value as number) > 100_000_000) {
    throw new DomainCommandError(`INVALID_${field.toUpperCase()}`, `${field} must be a positive integer within the supported limit.`);
  }
  return value as number;
}

export function optionalKrw(value: unknown, field: string): number | undefined {
  if (value === undefined || value === null) return undefined;
  return positiveKrw(value, field);
}

export function isoNow(date = new Date()): string {
  if (Number.isNaN(date.getTime())) throw new DomainCommandError('INVALID_SERVER_TIME', 'Server time is invalid.', 500);
  return date.toISOString();
}

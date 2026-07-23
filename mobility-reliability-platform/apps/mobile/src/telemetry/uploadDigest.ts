const LOWERCASE_SHA256 = /^[0-9a-f]{64}$/;

export function isLowercaseSha256(value: string): boolean {
  return LOWERCASE_SHA256.test(value);
}

export function requireLowercaseSha256(value: string): void {
  if (!isLowercaseSha256(value)) {
    throw new Error('UPLOAD_BATCH_DIGEST_INVALID');
  }
}

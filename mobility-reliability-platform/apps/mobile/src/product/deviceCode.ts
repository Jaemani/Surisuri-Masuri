export function normalizeDeviceCode(value: string) {
  return value.trim().toUpperCase().replace(/\s+/g, '');
}

export function parseDeviceQrValue(value: string): string | null {
  const trimmed = value.trim();
  const deepLink = /^surisuri:\/\/device\/([A-Za-z0-9_-]{2,64})$/i.exec(trimmed);
  if (deepLink) return normalizeDeviceCode(deepLink[1]);
  if (/^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/.test(trimmed)) return normalizeDeviceCode(trimmed);
  return null;
}


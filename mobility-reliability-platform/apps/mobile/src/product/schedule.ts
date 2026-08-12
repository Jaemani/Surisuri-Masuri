const MAX_SCHEDULE_DAYS = 180;

export function scheduleBounds(now = new Date()): { minimum: Date; maximum: Date } {
  return {
    minimum: new Date(now.getTime()),
    maximum: new Date(now.getTime() + MAX_SCHEDULE_DAYS * 24 * 60 * 60 * 1000),
  };
}

export function createDefaultSchedule(now = new Date()): Date {
  const candidate = new Date(now.getTime() + 24 * 60 * 60 * 1000);
  candidate.setHours(14, 0, 0, 0);
  if (candidate.getTime() <= now.getTime()) candidate.setDate(candidate.getDate() + 1);
  return candidate;
}

export function isScheduleInRange(value: Date, now = new Date()): boolean {
  if (Number.isNaN(value.getTime())) return false;
  const { minimum, maximum } = scheduleBounds(now);
  return value.getTime() >= minimum.getTime() && value.getTime() <= maximum.getTime();
}

export function formatScheduleSeoul(value: Date): string {
  return new Intl.DateTimeFormat('ko-KR', {
    timeZone: 'Asia/Seoul',
    month: 'long',
    day: 'numeric',
    weekday: 'short',
    hour: 'numeric',
    minute: '2-digit',
  }).format(value);
}

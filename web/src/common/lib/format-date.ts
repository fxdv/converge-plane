/**
 * Date/time display policy (Phase 2): use date-fns here for new code.
 * legacy `dayjs` / `javascript-time-ago` remain until touched.
 */
import { formatDistanceToNow, formatISO, parseISO } from 'date-fns';

export function formatRelativeTime(iso: string): string {
  return formatDistanceToNow(parseISO(iso), { addSuffix: true });
}

export function toIsoDate(date: Date): string {
  return formatISO(date, { representation: 'date' });
}

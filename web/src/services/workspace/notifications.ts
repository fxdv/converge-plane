import axios from 'axios';

import type { NotificationType } from 'common/types';

// The in-app inbox (docs/spec 12, SWR-13). The inbox's rows ride the
// sync feed (addressed delivery — the same seam as the board); these
// routes are the interactive verbs: the per-row read, the clear-all,
// and the list (a fresh read — the badge's truth is the database, not
// the feed). Same-origin through the web proxy, so the session cookie
// applies without CORS.

export async function listNotifications(
  workspaceId: string,
  opts?: { unreadOnly?: boolean; limit?: number },
): Promise<NotificationType[]> {
  const params: Record<string, string> = { workspaceId };
  if (opts?.unreadOnly) {
    params.unreadOnly = 'true';
  }
  if (opts?.limit) {
    params.limit = String(opts.limit);
  }
  const response = await axios.get<NotificationType[]>(
    '/api/v1/notifications',
    { params },
  );
  return response.data;
}

// The row must be the caller's (a notification is addressed, not
// shared); a double read is a server no-op that still answers 200.
export async function markNotificationRead(
  id: string,
): Promise<NotificationType> {
  const response = await axios.post<NotificationType>(
    `/api/v1/notifications/${id}/read`,
  );
  return response.data;
}

// The clear-all: the database marks every pending row; the feed's
// fan-out is capped server-side (100 records), the rest settles on the
// next list fetch or bootstrap.
export async function markAllNotificationsRead(
  workspaceId: string,
): Promise<{ updated: number }> {
  const response = await axios.post<{ updated: number }>(
    '/api/v1/notifications/read_all',
    null,
    { params: { workspaceId } },
  );
  return response.data;
}

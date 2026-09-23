import { types } from 'mobx-state-tree';

// The in-app inbox (docs/spec 12, SWR-13): one row is one recipient's
// one nudge — a pointer to the issue's trace (the row is disposable,
// the timeline is the record). recipientId is the delivery key: the
// plane's feed is per-workspace, so the client keeps only the rows
// addressed to its user.
export const Notification = types.model('Notification', {
  id: types.string,
  workspaceId: types.string,
  issueId: types.string,
  issueNumber: types.number,
  // Plain string on purpose: a new server type must degrade, never
  // crash model validation (the project has a history of exactly that
  // crash class).
  type: types.string,
  actorId: types.union(types.string, types.null),
  actorName: types.union(types.string, types.null),
  recipientId: types.string,
  createdAt: types.string,
  readAt: types.union(types.string, types.null),
});

export const NotificationArray = types.array(Notification);

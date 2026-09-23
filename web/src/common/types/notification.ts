// The in-app inbox (docs/spec 12, SWR-13): one row is one recipient's
// one nudge — a pointer to the issue's trace (the row is disposable;
// the issue's timeline is the record). The plane's feed is
// per-workspace, so the row carries its delivery key (recipientId) and
// the client keeps only the rows addressed to its user.
//
// The legacy Tegon notification type (IssueAssigned, actionData, ...)
// is gone with the M3 dead-feature pass; the inbox's trigger kinds are
// the server's vocabulary (assigned, reassigned, state, closed,
// comment, mention, handoff, pause) and ride as a plain string, so a
// new kind degrades instead of crashing the model.
export interface NotificationType {
  id: string;
  workspaceId: string;
  issueId: string;
  issueNumber: number;
  type: string;
  actorId: string | null;
  actorName: string | null;
  recipientId: string;
  createdAt: string;
  readAt: string | null;
}

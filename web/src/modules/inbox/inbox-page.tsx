import { Inbox } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import { formatDistanceToNow } from 'date-fns';
import { observer } from 'mobx-react-lite';
import { useParams } from 'next/navigation';
import { useRouter } from 'next/router';
import * as React from 'react';

import { HeaderLayout } from 'common/header-layout';
import { AppLayout } from 'common/layouts/app-layout';
import { MainLayout } from 'common/layouts/main-layout';
import type { NotificationType, TeamType } from 'common/types';
import { withApplicationStore } from 'common/wrappers/with-application-store';

import { useCurrentWorkspace } from 'hooks/workspace';

import {
  markAllNotificationsRead,
  markNotificationRead,
} from 'services/workspace';

import { convergeDatabase } from 'store/database';
import { useContextStore } from 'store/global-context-provider';

// spec cs:swarm:inbox
/** The inbox (docs/spec 12): where a human hears the swarm. One row is
 * one nudge addressed to the signed-in account — a pointer to the
 * issue's trace (the row is disposable; the timeline is the record).
 *
 * The rows come from the synced store (the addressed delivery: the feed
 * is per-workspace, the store keeps only this recipient's rows); the
 * REST verbs persist the read state. Visual language as on the plane:
 * hairline rules, no fills, tabular numerals, color reserved for
 * meaning (the unread dot is the only accent).
 */

// The trigger matrix's vocabulary, rendered as the human sentence. The
// type rides as a plain string on purpose: a new kind degrades to the
// generic line, never a crash.
function phrase(n: NotificationType, label: string): string {
  const actor = n.actorName || 'Someone';
  switch (n.type) {
    case 'assigned':
      return `${actor} assigned ${label} to you`;
    case 'reassigned':
      return `${actor} reassigned ${label}`;
    case 'state':
      return `${actor} moved ${label}`;
    case 'closed':
      return `${actor} closed ${label}`;
    case 'comment':
      return `${actor} commented on ${label}`;
    case 'mention':
      return `${actor} mentioned you on ${label}`;
    case 'handoff':
      return `${actor} handed ${label} to you`;
    case 'pause':
      return `${actor} paused ${label} for review`;
    default:
      return `${actor} · ${n.type} · ${label}`;
  }
}

const InboxRow = observer(
  ({
    notification,
    workspaceSlug,
    onRead,
  }: {
    notification: NotificationType;
    workspaceSlug: string | undefined;
    onRead: (n: NotificationType) => void;
  }) => {
    const { issuesStore, teamsStore } = useContextStore();
    const router = useRouter();

    // The label: the issue's number, prefixed when the issue is in this
    // tab's map (its team's identifier). A missing map entry degrades to
    // the bare number — the row stays readable, the link stays live.
    const issue = issuesStore.issuesMap.get(notification.issueId);
    // The store context is untyped (IAnyStateTreeNode), so the callback
    // parameter carries its type explicitly — the house pattern.
    const team = issue
      ? teamsStore.teams.find((t: TeamType) => t.id === issue.teamId)
      : undefined;
    const label = team
      ? `${team.identifier}-${notification.issueNumber}`
      : `#${notification.issueNumber}`;

    const unread = notification.readAt === null;

    return (
      <li
        className={cn(
          'flex items-baseline gap-3 px-4 py-2 border-t border-grayAlpha-100 dark:border-grayAlpha-300 first:border-t-0',
          !unread && 'text-muted-foreground',
        )}
      >
        <span
          className={cn(
            'h-1.5 w-1.5 rounded-full shrink-0 self-center',
            unread ? 'bg-emerald-500 dark:bg-emerald-400' : 'bg-transparent',
          )}
        />
        <button
          type="button"
          className="font-mono text-sm tabular-nums hover:underline text-left"
          onClick={() => {
            if (workspaceSlug) {
              router.push(`/${workspaceSlug}/issue/${notification.issueId}`);
            }
          }}
        >
          {label}
        </button>
        <span className="text-sm truncate">{phrase(notification, label)}</span>
        <span className="ml-auto text-xs text-muted-foreground shrink-0">
          {formatDistanceToNow(new Date(notification.createdAt), {
            addSuffix: true,
          })}
        </span>
        {unread && (
          <button
            type="button"
            className="text-xs underline shrink-0"
            onClick={() => onRead(notification)}
          >
            read
          </button>
        )}
      </li>
    );
  },
);

export const InboxPage = withApplicationStore(() => {
  const { notificationsStore } = useContextStore();
  const workspace = useCurrentWorkspace();
  const { workspaceSlug } = useParams<{ workspaceSlug: string }>();

  const rows = workspace ? notificationsStore.forWorkspace(workspace.id) : [];
  const unread = workspace ? notificationsStore.unreadIn(workspace.id) : [];

  // The read verbs persist server-side; the store update is the snappy
  // half (the feed's own UPDATE record upserts the same id, so the two
  // never diverge).
  const onRead = async (n: NotificationType) => {
    try {
      const updated = await markNotificationRead(n.id);
      notificationsStore.update(updated);
      await convergeDatabase.notifications.put(updated);
    } catch {
      // The row stays unread; the feed or the next list fetch settles it.
    }
  };

  const onReadAll = async () => {
    if (!workspace) {
      return;
    }
    const stamp = new Date().toISOString();
    for (const n of unread) {
      notificationsStore.update({ ...n, readAt: stamp });
      await convergeDatabase.notifications.put({ ...n, readAt: stamp });
    }
    try {
      await markAllNotificationsRead(workspace.id);
    } catch {
      // The rows stay read locally; the server reconciles on the next
      // bootstrap (the badge's truth is the database).
    }
  };

  return (
    <MainLayout
      header={
        <HeaderLayout>
          <h3 className="flex items-center gap-2">
            <Inbox className="h-4 w-4" /> Inbox
            {unread.length > 0 && (
              <span className="text-xs font-normal text-muted-foreground tabular-nums">
                {unread.length} unread
              </span>
            )}
          </h3>
        </HeaderLayout>
      }
    >
      <div className="p-6 max-w-3xl mx-auto w-full">
        <div className="border border-grayAlpha-100 dark:border-grayAlpha-300">
          {rows.length === 0 ? (
            <div className="p-6 text-sm text-muted-foreground leading-relaxed">
              Nothing here. When something touches a card you are on — an
              assign, a move, a comment, a mention, a handoff, a pause — it
              lands here. Older than 90 days it is purged; the issue&apos;s
              timeline keeps the full record.
            </div>
          ) : (
            <>
              <div className="px-4 py-2 border-b border-grayAlpha-100 dark:border-grayAlpha-300 flex items-center justify-between">
                <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Newest first
                </span>
                {unread.length > 0 && (
                  <button
                    type="button"
                    className="text-xs underline"
                    onClick={() => {
                      void onReadAll();
                    }}
                  >
                    Mark all as read
                  </button>
                )}
              </div>
              <ul>
                {rows.map((n: NotificationType) => (
                  <InboxRow
                    key={n.id}
                    notification={n}
                    workspaceSlug={workspaceSlug}
                    onRead={(row) => {
                      void onRead(row);
                    }}
                  />
                ))}
              </ul>
            </>
          )}
        </div>
        <div className="mt-2 px-4 text-[11px] text-muted-foreground">
          Addressed to your account only · a new day starts a new nudge for the
          same event · the issue&apos;s timeline is the record, this is the
          pointer
        </div>
      </div>
    </MainLayout>
  );
});

InboxPage.getLayout = function getLayout(page: React.ReactElement) {
  return <AppLayout>{page}</AppLayout>;
};

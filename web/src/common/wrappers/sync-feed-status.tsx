import { Button } from '@converge/ui/components/button';
import * as React from 'react';

export type SyncFeedStatus = 'live' | 'catching-up' | 'stale';

type SyncFeedContextValue = {
  status: SyncFeedStatus;
  setStatus: (status: SyncFeedStatus) => void;
};

const SyncFeedContext = React.createContext<SyncFeedContextValue | null>(null);

export function SyncFeedProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = React.useState<SyncFeedStatus>('live');
  const value = React.useMemo(
    () => ({ status, setStatus }),
    [status],
  );

  return (
    <SyncFeedContext.Provider value={value}>{children}</SyncFeedContext.Provider>
  );
}

export function useSyncFeedStatus(): SyncFeedContextValue {
  const ctx = React.useContext(SyncFeedContext);
  if (!ctx) {
    throw new Error('useSyncFeedStatus requires SyncFeedProvider');
  }
  return ctx;
}

/** Shown when the realtime feed is repairing a gap or needs a full reload. */
export function SyncFeedBanner() {
  const { status } = useSyncFeedStatus();

  if (status === 'live') {
    return null;
  }

  const stale = status === 'stale';

  return (
    <div
      role="status"
      className={
        stale
          ? 'shrink-0 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-foreground flex items-center justify-between gap-4'
          : 'shrink-0 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm text-foreground flex items-center justify-between gap-4'
      }
    >
      <span>
        {stale
          ? 'This workspace is out of date. Reload to pick up everything the server kept.'
          : 'Catching up with the server…'}
      </span>
      {stale && (
        <Button
          variant="secondary"
          size="sm"
          onClick={() => window.location.reload()}
        >
          Reload workspace
        </Button>
      )}
    </div>
  );
}

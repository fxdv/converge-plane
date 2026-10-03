import { Button } from '@converge/ui/components/button';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import * as React from 'react';

type ConflictState = {
  issueLabel: string;
};

type IssueConflictContextValue = {
  openConflict: (state: ConflictState) => void;
};

const IssueConflictContext =
  React.createContext<IssueConflictContextValue | null>(null);

export function IssueConflictProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [state, setState] = React.useState<ConflictState | null>(null);

  const value = React.useMemo(
    () => ({
      openConflict: (next: ConflictState) => setState(next),
    }),
    [],
  );

  return (
    <IssueConflictContext.Provider value={value}>
      {children}
      <Dialog open={state !== null} onOpenChange={(open) => !open && setState(null)}>
        <DialogContent className="sm:max-w-[420px]" closeIcon>
          <div className="p-6">
            <DialogHeader>
              <DialogTitle className="text-md font-normal">
                {state ? `${state.issueLabel} changed elsewhere` : 'Issue changed'}
              </DialogTitle>
            </DialogHeader>
            <p className="mt-3 text-sm text-muted-foreground">
              Someone else or an agent saved a newer version while you were
              editing. Your last change was not kept. Reload this issue to see
              what is on the server now.
            </p>
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setState(null)}>
                Keep browsing
              </Button>
              <Button onClick={() => window.location.reload()}>Reload</Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </IssueConflictContext.Provider>
  );
}

export function useIssueConflict(): IssueConflictContextValue {
  const ctx = React.useContext(IssueConflictContext);
  if (!ctx) {
    throw new Error('useIssueConflict requires IssueConflictProvider');
  }
  return ctx;
}

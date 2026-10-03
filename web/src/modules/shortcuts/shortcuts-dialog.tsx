import { Button } from '@converge/ui/components/button';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import * as React from 'react';

import { KEYBOARD_SHORTCUT_SECTIONS } from './keyboard-shortcuts';

type ShortcutsDialogContextValue = {
  open: boolean;
  setOpen: (open: boolean) => void;
  openDialog: () => void;
};

const ShortcutsDialogContext =
  React.createContext<ShortcutsDialogContextValue | null>(null);

export function ShortcutsDialogProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(false);
  const value = React.useMemo(
    () => ({
      open,
      setOpen,
      openDialog: () => setOpen(true),
    }),
    [open],
  );

  return (
    <ShortcutsDialogContext.Provider value={value}>
      {children}
      <ShortcutsDialog open={open} onOpenChange={setOpen} />
    </ShortcutsDialogContext.Provider>
  );
}

export function useShortcutsDialog(): ShortcutsDialogContextValue {
  const ctx = React.useContext(ShortcutsDialogContext);
  if (!ctx) {
    throw new Error('useShortcutsDialog requires ShortcutsDialogProvider');
  }
  return ctx;
}

export function ShortcutsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]" closeIcon>
        <div className="p-6">
          <DialogHeader>
            <DialogTitle className="text-md text-foreground font-normal">
              Keyboard shortcuts
            </DialogTitle>
          </DialogHeader>
          <div className="mt-4 flex flex-col gap-5">
            {KEYBOARD_SHORTCUT_SECTIONS.map((section) => (
              <div key={section.title}>
                <p className="text-xs font-medium text-muted-foreground mb-2">
                  {section.title}
                </p>
                <ul className="flex flex-col gap-2 text-sm">
                  {section.rows.map((row) => (
                    <li
                      key={`${section.title}-${row.keys}`}
                      className="flex justify-between gap-4"
                    >
                      <span>{row.label}</span>
                      <span className="font-mono text-muted-foreground shrink-0">
                        {row.keys}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
          <div className="mt-4 flex justify-end">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

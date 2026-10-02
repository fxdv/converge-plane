import { Button } from '@converge/ui/components/button';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import React from 'react';
import { useHotkeys } from 'react-hotkeys-hook';
import { Key } from 'ts-key-enum';

import { SearchDialog } from 'modules/search';

import { SCOPES } from 'common/scopes';

import { CommandDialog } from './command';

const SHORTCUTS: Array<[string, string]> = [
  ['C', 'Create issue'],
  ['F', 'Filter'],
  ['?', 'This list'],
  ['Esc', 'Close'],
  ['⌘/Ctrl+Enter', 'Submit'],
  ['⌘/Ctrl+K', 'Command palette'],
  ['⌘/Ctrl+/', 'Search'],
];

interface State {
  newIssue: boolean;
  search: boolean;
  help: boolean;
}

const defaultState: State = {
  newIssue: false,
  search: false,
  help: false,
};

export function GlobalShortcuts() {
  const [state, setState] = React.useState<State>(defaultState);

  const stateChange = (value: boolean, forDialog: string) => {
    setState((currentState) => ({ ...currentState, [forDialog]: value }));
  };

  // react-hotkeys-hook matches key codes, and the "/" key's code is Slash.
  useHotkeys(
    [`${Key.Meta}+slash`, `${Key.Control}+slash`],
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (e: any) => {
      stateChange(true, 'search');

      e.preventDefault();
    },
    { scopes: [SCOPES.Global] },
  );

  useHotkeys(
    ['shift+/', '?'],
    (e) => {
      stateChange(true, 'help');
      e.preventDefault();
    },
    { scopes: [SCOPES.Global] },
  );

  return (
    <>
      <SearchDialog
        open={state.search}
        setOpen={(value) => stateChange(value, 'search')}
      />
      <CommandDialog
        setDialogState={(value: string) => stateChange(true, value)}
      />
      <Dialog
        open={state.help}
        onOpenChange={(open) => stateChange(open, 'help')}
      >
        <DialogContent className="sm:max-w-[360px]" closeIcon>
          <div className="p-6">
            <DialogHeader>
              <DialogTitle className="text-md text-foreground font-normal">
                Shortcuts
              </DialogTitle>
            </DialogHeader>
            <ul className="mt-4 flex flex-col gap-2 text-sm">
              {SHORTCUTS.map(([keys, label]) => (
                <li key={keys} className="flex justify-between gap-4">
                  <span>{label}</span>
                  <span className="font-mono text-muted-foreground">
                    {keys}
                  </span>
                </li>
              ))}
            </ul>
            <div className="mt-4 flex justify-end">
              <Button
                variant="ghost"
                onClick={() => stateChange(false, 'help')}
              >
                Close
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

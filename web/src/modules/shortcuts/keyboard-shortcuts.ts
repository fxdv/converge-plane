/** Canonical in-app keyboard map (Phase 0). Keep in sync with hotkey handlers. */

export type KeyboardShortcutRow = {
  keys: string;
  label: string;
};

export type KeyboardShortcutSection = {
  title: string;
  rows: KeyboardShortcutRow[];
};

export const KEYBOARD_SHORTCUT_SECTIONS: KeyboardShortcutSection[] = [
  {
    title: 'Everywhere',
    rows: [
      { keys: 'C', label: 'Create issue' },
      { keys: '⌘/Ctrl+K', label: 'Command palette' },
      { keys: '⌘/Ctrl+/', label: 'Search issues' },
      { keys: '?', label: 'This list' },
      { keys: 'Esc', label: 'Close dialog or side issue' },
    ],
  },
  {
    title: 'Board',
    rows: [
      { keys: 'O', label: 'Open the first issue (focus title)' },
      { keys: 'F', label: 'Toggle filter bar' },
      { keys: 'S', label: 'Change status (selected issues)' },
      { keys: 'A', label: 'Change assignee (selected issues)' },
      { keys: 'L', label: 'Change labels (selected issues)' },
      { keys: 'P', label: 'Change priority (selected issues)' },
    ],
  },
  {
    title: 'Issue',
    rows: [
      { keys: 'S', label: 'Edit status' },
      { keys: 'M', label: 'Focus comment' },
      { keys: 'U', label: 'Return to the board' },
      { keys: '⌘/Ctrl+Enter', label: 'Submit comment or form' },
    ],
  },
];

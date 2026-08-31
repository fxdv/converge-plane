import type { EditorT } from '@converge/ui/components/editor/index';

import { useContextStore } from 'store/global-context-provider';

// Updates the height of a <textarea> when the value changes.
export const useEditorPasteHandler = () => {
  const { issuesStore, teamsStore } = useContextStore();

  const handlePaste = (editor: EditorT, event: ClipboardEvent) => {
    const pastedText = event.clipboardData.getData('text/plain');
    const regex = new RegExp(
      `https?://${window.location.host}/\\w+/issue/([A-Z]+)-(\\d+)`,
    );
    const isConvergeIssue = regex.test(pastedText);
    const parts = regex.exec(pastedText);
    if (isConvergeIssue && parts) {
      const teamIdentifier = parts[1]; // 'ENG' in this case
      const issueId = parts[2]; // '11' in this case
      const team = teamsStore.getTeamWithIdentifier(teamIdentifier);
      const issue = team
        ? issuesStore.getIssueByNumber(`${teamIdentifier}-${issueId}`, team.id)
        : undefined;

      if (issue) {
        editor
          .chain()
          .insertContentAt(editor.view.state.selection.from, [
            {
              type: 'convergeIssueExtension',
              attrs: {
                url: pastedText,
              },
            },
          ])
          .exitCode()
          .focus()
          .run();

        return true;
      }
    }

    return false;
  };

  return { handlePaste };
};

import { Editor, EditorExtensions } from '@converge/ui/components/editor/index';
import { ScrollArea } from '@converge/ui/components/scroll-area';
import { Separator } from '@converge/ui/components/separator';
import { observer } from 'mobx-react-lite';
import * as React from 'react';
import { useDebouncedCallback } from 'use-debounce';

import { useEditorSuggestionItems } from 'modules/issues/components/use-editor-suggestion-items';

import { getTiptapJSON } from 'common';
import { convergeIssueExtension } from 'common/editor/converge-issue-extension';

import { useIssueData } from 'hooks/issues';
import { useEditorPasteHandler } from 'hooks/use-editor-paste-handler';

import { useUpdateIssueMutation } from 'services/issues';

import { Activity } from './activity';
import { FileUpload } from './file-upload';
import { IssueSubIssueSelector } from './issue-sub-issue-selector';
import { IssueTitle } from './issue-title';
import { ParentIssueView } from './parent-issue-view';
import { RelatedIssueView } from './related-issue-view';
import { SubIssueView } from './sub-issue-view';

export const LeftSide = observer(() => {
  const issue = useIssueData();
  const { mutate: updateIssue } = useUpdateIssueMutation({});
  const { suggestionItems } = useEditorSuggestionItems();

  const onDescriptionChange = useDebouncedCallback((content: string) => {
    const { json: description } = getTiptapJSON(content);

    updateIssue({
      description: JSON.stringify(description),
      teamId: issue.teamId,
      id: issue.id,
    });
  }, 1000);

  const onIssueChange = useDebouncedCallback((content: string) => {
    updateIssue({
      title: content,
      teamId: issue.teamId,
      id: issue.id,
    });
  }, 1000);

  const { handlePaste } = useEditorPasteHandler();

  return (
    <ScrollArea className="grow flex h-full justify-center w-full">
      <div className="flex h-full justify-center w-full pb-[150px]">
        <div className="grow flex flex-col gap-2 h-full max-w-[97ch]">
          <div className="py-6 flex flex-col">
            <IssueTitle value={issue.title} onChange={onIssueChange} />
            {issue.parentId && (
              <div className="px-6">
                <ParentIssueView issue={issue} />
              </div>
            )}
            <Editor
              value={issue.description}
              onChange={onDescriptionChange}
              handlePaste={handlePaste}
              extensions={[convergeIssueExtension]}
              className="min-h-[50px] mb-8 px-6 mt-3 text-md"
            >
              <FileUpload />
              <EditorExtensions suggestionItems={suggestionItems}>
                <IssueSubIssueSelector />
              </EditorExtensions>
            </Editor>

            <div className="mx-6">
              <Separator />
            </div>
            <SubIssueView childIssues={issue.children} issueId={issue.id} />

            <RelatedIssueView issue={issue} />

            <Activity />
          </div>
        </div>
      </div>
    </ScrollArea>
  );
});

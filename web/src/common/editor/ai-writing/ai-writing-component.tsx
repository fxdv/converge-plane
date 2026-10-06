import { Button } from '@converge/ui/components/button';
import { Card, CardContent } from '@converge/ui/components/card';
import { Markdown, useEditor } from '@converge/ui/components/editor/index';
import { Loader } from '@converge/ui/components/loader';
import { Skeleton } from '@converge/ui/components/skeleton';
import { Textarea } from '@converge/ui/components/textarea';
import { AI, CheckLine, DeleteLine } from '@converge/ui/icons';
import { NodeViewWrapper } from '@tiptap/react';
import React from 'react';

import { useCurrentWorkspace } from 'hooks/workspace';

import { useAIContinueWritingMutation } from 'services/issues';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const AIWritingComponent = (props: any) => {
  const { editor } = useEditor();
  const [prompt, setPrompt] = React.useState('Continue writing');
  const { responses, mutate, isPending: isLoading } =
    useAIContinueWritingMutation();
  const workspace = useCurrentWorkspace();
  const initialDescription = props.node.attrs.content as string | undefined;

  React.useEffect(() => {
    if (!initialDescription || !workspace?.id) {
      return;
    }
    mutate({
      description: initialDescription,
      workspaceId: workspace.id,
      userInput: 'Continue writing',
    });
  }, [initialDescription, workspace?.id, mutate]);

  return (
    <NodeViewWrapper className="ai-writing-component">
      <Card className="my-2">
        <CardContent className="p-2">
          <p className="text-sm">Preview</p>

          <Markdown>{responses}</Markdown>
          {(isLoading || !responses) && (
            <div className="flex flex-col gap-2 my-2">
              <Skeleton className="w-full h-5" />
              <Skeleton className="w-full h-5" />
              <Skeleton className="w-full h-5" />
            </div>
          )}

          <div className="flex flex-col gap-2 mt-4">
            <label className="text-sm"> Prompt </label>
            <Textarea
              value={prompt}
              className="min-h-24"
              onChange={(e) => setPrompt(e.currentTarget.value)}
            />
            <div className="flex justify-end items-center">
              <div className="flex items-center gap-2">
                {isLoading && (
                  <Loader text="Thinking..." variant="horizontal" />
                )}
                <Button
                  variant="ghost"
                  className="flex items-center gap-2"
                  onClick={() => {
                    props.deleteNode();
                  }}
                >
                  <DeleteLine size={16} />
                  Discard
                </Button>
                <Button
                  variant="ghost"
                  className="flex items-center gap-2"
                  disabled={isLoading}
                  onClick={() => {
                    props.deleteNode();
                    editor.commands.insertContent(responses);
                  }}
                >
                  <CheckLine size={16} />
                  Insert
                </Button>
                <Button
                  variant="secondary"
                  className="flex items-center gap-2"
                  disabled={isLoading}
                  onClick={() => {
                    mutate({
                      description: props.node.attrs.content,
                      workspaceId: workspace.id,
                      userInput: prompt,
                    });
                  }}
                >
                  <AI size={16} />
                  Regenerate
                </Button>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>
    </NodeViewWrapper>
  );
};

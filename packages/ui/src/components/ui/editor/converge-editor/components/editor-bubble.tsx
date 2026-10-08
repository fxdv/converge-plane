import type { Editor } from '@tiptap/core';
import type { ReactNode } from 'react';

import { NodeSelection } from '@tiptap/pm/state';
import { useCurrentEditor } from '@tiptap/react';
import { BubbleMenu, type BubbleMenuProps } from '@tiptap/react/menus';
import { forwardRef, useMemo } from 'react';

export interface EditorBubbleProps extends Omit<
  BubbleMenuProps,
  'editor' | 'children'
> {
  readonly children: ReactNode;
  /** @deprecated TipTap 3 uses Floating UI; kept for call-site compatibility. */
  tippyOptions?: { placement?: string };
}

export const EditorBubble = forwardRef<HTMLDivElement, EditorBubbleProps>(
  ({ children, tippyOptions, ...rest }, ref) => {
    const { editor: currentEditor } = useCurrentEditor();

    const bubbleMenuProps: Omit<BubbleMenuProps, 'children'> = useMemo(() => {
      const shouldShow: BubbleMenuProps['shouldShow'] = ({ editor, state }) => {
        const { selection } = state;
        const { empty } = selection;

        if (
          !editor.isEditable ||
          editor.isActive('image') ||
          empty ||
          selection instanceof NodeSelection
        ) {
          return false;
        }
        return true;
      };

      return {
        shouldShow,
        editor: currentEditor as Editor,
        options: {
          placement:
            (tippyOptions?.placement as NonNullable<
              BubbleMenuProps['options']
            >['placement']) ?? 'top',
        },
        ...rest,
      };
    }, [currentEditor, rest, tippyOptions?.placement]);

    if (!currentEditor) {
      return null;
    }

    return (
      <div ref={ref}>
        <BubbleMenu {...bubbleMenuProps}>{children}</BubbleMenu>
      </div>
    );
  },
);

EditorBubble.displayName = 'EditorBubble';

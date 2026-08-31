import { mergeAttributes, Node } from '@tiptap/core';
import { ReactNodeViewRenderer } from '@tiptap/react';

import { ConvergeIssueComponent } from './converge-issue-component';

export const convergeIssueExtension = Node.create({
  name: 'convergeIssueExtension',
  group: 'inline',
  inline: true,
  atom: true,

  addAttributes() {
    return {
      url: {
        default: undefined,
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: 'converge-issue-extension',
      },
    ];
  },

  renderHTML({ HTMLAttributes }) {
    return ['converge-issue-extension', mergeAttributes(HTMLAttributes)];
  },

  addNodeView() {
    return ReactNodeViewRenderer(ConvergeIssueComponent, {
      contentDOMElementTag: 'span',
      as: 'span',
    });
  },
});

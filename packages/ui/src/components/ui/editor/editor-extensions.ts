import { Extension, mergeAttributes } from '@tiptap/core';
import CodeBlock from '@tiptap/extension-code-block';
import Heading from '@tiptap/extension-heading';
import { cx } from 'class-variance-authority';

import {
  TiptapLink,
  TaskList,
  TaskItem,
  HorizontalRule,
  StarterKit,
  Placeholder,
  HighlightExtension,
  AIHighlight,
  MarkdownExtension,
} from './converge-editor';
import { fileExtension } from './file-extension';
import { imageExtension } from './image-extension';
import { isAllowedLinkUri } from './link-protocols';

// Explicit protocol allowlist (http, https, mailto): Tiptap applies
// isAllowedUri when parsing HTML, rendering, and in setLink/toggleLink, so
// javascript:, data: and vbscript: hrefs never become a live link.
const tiptapLink = TiptapLink.configure({
  HTMLAttributes: {
    class: cx('text-primary cursor-pointer'),
  },
  isAllowedUri: (url, { defaultValidate }) =>
    isAllowedLinkUri(url) && defaultValidate(url),
  shouldAutoLink: (url) => isAllowedLinkUri(url),
});

const taskList = TaskList.configure({
  HTMLAttributes: {
    class: cx('not-prose'),
  },
});

const taskItem = TaskItem.configure({
  HTMLAttributes: {
    class: cx('flex items-start gap-1 my-2'),
  },
  nested: true,
});

const horizontalRule = HorizontalRule.configure({
  HTMLAttributes: {
    class: cx('mt-4 mb-6 border-t border-muted-foreground'),
  },
});

const heading = Heading.extend({
  renderHTML({ node, HTMLAttributes }) {
    const hasLevel = this.options.levels.includes(node.attrs.level);
    const level: 1 | 2 | 3 = hasLevel
      ? node.attrs.level
      : this.options.levels[0];
    const levelMap = { 1: 'text-xl', 2: 'text-lg', 3: 'text-md' };

    return [
      `h${level}`,
      mergeAttributes(this.options.HTMLAttributes, HTMLAttributes, {
        class: `h${node.attrs.level}-style ${levelMap[level]} my-2`,
      }),
      0,
    ];
  },
}).configure({ levels: [1, 2, 3] });

const starterKit = StarterKit.configure({
  heading: false,
  codeBlock: false,
  // Tiptap 3's StarterKit bundles Link; the configured tiptapLink below is
  // the only one, so its protocol allowlist cannot be bypassed by a twin.
  link: false,
  bulletList: {
    HTMLAttributes: {
      class: cx('list-disc list-outside leading-3 pl-4'),
    },
  },
  orderedList: {
    HTMLAttributes: {
      class: cx('list-decimal list-outside pl-8 leading-3'),
    },
  },
  listItem: {
    HTMLAttributes: {
      class: cx('leading-normal'),
    },
  },
  blockquote: {
    HTMLAttributes: {
      class: cx('border-l-4 border-gray-400 dark:border-gray-500'),
    },
  },
  code: false,
  horizontalRule: false,
  dropcursor: {
    color: 'oklch(60% 0.13 240)',
    width: 4,
  },
  gapcursor: false,
});

// html: false — markdown strings reach the editor only from agent/API
// writes (plain-string descriptions); the app itself stores Tiptap JSON,
// and clipboard HTML paste does not go through this parser. Raw HTML in
// those strings renders as text instead of being parsed into nodes.
const markdown = MarkdownExtension.configure({
  html: false,
  breaks: true,
});

const defaultPlaceholder = Placeholder.configure({
  placeholder: ({ node }) => {
    if (node.type.name === 'heading') {
      return `Heading ${node.attrs.level}`;
    }
    if (node.type.name === 'image' || node.type.name === 'table') {
      return '';
    }
    if (node.type.name === 'codeBlock') {
      return 'Type in your code here...';
    }

    return 'Describe your issue...';
  },
  includeChildren: true,
});

export const getPlaceholder = (placeholder: string | Extension) => {
  if (!placeholder) {
    return defaultPlaceholder;
  }

  if (typeof placeholder === 'string') {
    return Placeholder.configure({
      placeholder: () => {
        return placeholder;
      },
      includeChildren: true,
    });
  }

  return placeholder;
};

const codeBlock = CodeBlock.configure({
  HTMLAttributes: {
    class: cx(
      'rounded-md bg-grayAlpha-100 text-[#BF4594] px-1.5 py-1 font-mono font-medium border-none box-decoration-clone',
    ),
  },
});

export const defaultExtensions = [
  starterKit,
  tiptapLink,
  taskList,
  taskItem,
  horizontalRule,
  heading,
  AIHighlight,
  fileExtension,
  imageExtension,
  markdown,
  codeBlock,
  HighlightExtension,
];

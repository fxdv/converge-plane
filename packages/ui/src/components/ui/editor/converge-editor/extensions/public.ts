import Highlight from '@tiptap/extension-highlight';
import { Markdown } from 'tiptap-markdown';

export { default as TiptapLink } from '@tiptap/extension-link';
export { TaskItem } from '@tiptap/extension-task-item';
export { default as TaskList } from '@tiptap/extension-task-list';
export { default as StarterKit } from '@tiptap/starter-kit';
export { default as Placeholder } from '@tiptap/extension-placeholder';

export { AIHighlight } from './ai-highlight';
export { HorizontalRuleExtension as HorizontalRule } from './horizontal-rule';
export { ImageResizer } from './image-resizer';
export {
  Command,
  createSuggestionItems,
  handleCommandNavigation,
  type SuggestionItem,
} from './slash-command';

export const HighlightExtension = Highlight.configure({
  multicolor: true,
});

export const MarkdownExtension = Markdown;

export type { Editor as EditorInstance } from '@tiptap/core';
export { useCurrentEditor as useEditor } from '@tiptap/react';

export { EditorContent } from './components/editor-content';
export type { EditorContentProps } from './components/editor-content';
export { EditorBubble } from './components/editor-bubble';
export { EditorBubbleItem } from './components/editor-bubble-item';

export * from './extensions/public';
export type { ImageUploadOptions, UploadFn } from './plugins/upload-images';
export {
  createImageUpload,
  handleImageDrop,
  handleImagePaste,
} from './plugins/upload-images';

import { type IAnyStateTreeNode, types } from 'mobx-state-tree';

export const defaultCommonStoreValue: {
  chatOpen: boolean;
} = {
  chatOpen: false,
};

export const CommonStore: IAnyStateTreeNode = types
  .model({
    chatOpen: types.boolean,
    currentConversationId: types.union(types.undefined, types.string),
    conversationStreaming: types.union(types.undefined, types.boolean),
  })
  .actions((self) => ({
    update(data: Partial<CommonStoreState>) {
      // The model's properties are settable (MST observes the writes);
      // the state type bounds the keys so a stray field is a compile
      // error instead of a silent extra property.
      Object.assign(self, data);
    },
  }));

export interface CommonStoreState {
  chatOpen: boolean;
  currentConversationId?: string;
  conversationStreaming?: boolean;
}

export interface CommonStoreType {
  chatOpen: boolean;
  currentConversationId?: string;
  conversationStreaming?: boolean;
  update: (data: Partial<CommonStoreState>) => void;
}

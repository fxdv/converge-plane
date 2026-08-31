const enum Action {
  'I' = 'I',
  'U' = 'U',
  'D' = 'D',
}

export interface SyncActionRecord {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;

  modelName: string;
  modelId: string;
  action: Action;
  workspaceId: string;
  sequenceId: string;
}

export interface BootstrapResponse {
  syncActions: SyncActionRecord[];
  lastSequenceId: string;
  // The server sets this when the delta cannot be complete (its change
  // feed was trimmed past the client's cursor); the client must then
  // fall back to a full bootstrap.
  stale?: boolean;
}

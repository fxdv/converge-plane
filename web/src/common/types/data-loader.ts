// The client's sync-action vocabulary. The server's outbox maps its
// internal CREATE/UPDATE/DELETE onto exactly these values (the server's
// wireAction), and the client's save-data handlers switch on them.
// A literal union rather than an enum: it is used in type position only,
// and a const enum would refuse literal assignment from other files.
export type Action = 'I' | 'U' | 'D';

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

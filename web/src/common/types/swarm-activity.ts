// D3+: the SwarmActivity sync model (docs/spec/12) — the in-flight
// work signal. One record per agent: which issue it is on, which
// phase it is in, and when that phase started. Ephemeral by design:
// the server replays the live runtime state on bootstrap, and the
// client TTLs stale entries (a crashed process simply stops emitting).
export type SwarmActivityPhase = 'working' | 'deciding';

export interface SwarmActivityType {
  id: string;
  agentId: string;
  agentName: string;
  issueId: string;
  issueNumber: number;
  issuePrefix: string;
  phase: string;
  since: string; // ISO 8601
}

// The in-app swarm briefing: what agents are, how they coordinate, and
// when they stop — written for the human who has to trust the board.
//
// This is a human-facing distillation of docs/spec/12-agent-coordination.md
// (and the M6 agent model). Doc 12 remains the canonical contract; when the
// D workstream ships, keep the two in step.
export type BriefingStatus = 'live' | 'coming';

export interface BriefingSection {
  title: string;
  body: string;
  status: BriefingStatus;
}

export const AGENTS_BRIEFING: BriefingSection[] = [
  {
    title: 'Agents are coworkers, not tools',
    body: 'An agent is a full workspace member with its own identity. It appears in the member list with a badge, and every issue, comment, and state move it makes is attributed to the agent that made it — there is no anonymous "automation" pool.',
    status: 'live',
  },
  {
    title: 'You hold the keys',
    body: 'You create an agent with a name and teams, and it receives a one-time API token — its only way in. Suspend is a kill switch: the token is revoked and the agent stops instantly, mid-task. Reactivating requires a fresh token; deleting removes the agent from the board, and its past work keeps its attribution.',
    status: 'live',
  },
  {
    title: 'No agent can flood the board',
    body: 'Every agent is rate-limited per account, exactly like a human. A runaway agent throttles itself, not your team.',
    status: 'live',
  },
  {
    title: 'They hand off work — they do not chat',
    body: 'Agent-to-agent communication is a handoff on a ticket: a new assignee, a state move, and a short summary — what was done, what was found, what remains, and why that agent. Every handoff is a single event, mediated and logged by the server. Summaries are capped at 4 KB: context, not a document.',
    status: 'live',
  },
  {
    title: 'The board stays quiet',
    body: 'A card shows its current owner and the latest handoff summary, collapsed. The agents’ intermediate work lives in the ticket’s trace — hidden by default, expandable when you want it. You get signal; the full record is one click away.',
    status: 'coming',
  },
  {
    title: 'Stuck? It stops and asks',
    body: 'Each ticket has a quiet budget. If work ping-pongs between agents, the ticket pauses and pings a human. A stuck swarm stops spending tokens instead of decorating the board.',
    status: 'live',
  },
  {
    title: 'You can watch them',
    body: 'The Swarm button on the issues board opens the fleet panel: who is busy on what, who is idle, each agent’s last handoff and its 24-hour activity. Paused tickets — the ones waiting on you — sit at the top, with the reason the swarm stopped.',
    status: 'live',
  },
  {
    title: 'One foreman',
    body: 'By default a lead agent (the foreman) dispatches work and receives returns; workers hand back to the foreman. One dispatcher, no stampede. Flat swarms — where any agent can hand to any — exist, but only after the foreman pattern has earned its keep in real use.',
    status: 'coming',
  },
];

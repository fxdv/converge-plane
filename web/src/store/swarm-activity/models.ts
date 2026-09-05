import { types, type Instance } from 'mobx-state-tree';

// The client's mirror of the server's SwarmActivityData wire shape
// (docs/spec/12). `phase` is deliberately a plain string: a server
// that adds a new phase must degrade (ignored), never crash the
// client's model validation.
export const SwarmActivity = types.model('SwarmActivity', {
  id: types.string,
  agentId: types.string,
  agentName: types.string,
  issueId: types.string,
  issueNumber: types.number,
  issuePrefix: types.string,
  phase: types.string,
  since: types.string,
});

export type SwarmActivityType = Instance<typeof SwarmActivity>;

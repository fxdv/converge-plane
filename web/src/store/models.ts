// The models the client syncs. v1 scope (docs/spec 09/11): the issue
// loop, workflow, labels, members, saved views, and projects (v1.1).
// Models for forked features outside the active release (AI, actions,
// integrations, support/CRM, cycles, notifications, ...) were removed in
// the M3 dead-feature pass — the server still tolerates unknown model
// names in sync requests, but the client only asks for what it ships.
export enum MODELS {
  Workspace = 'Workspace',
  Team = 'Team',
  Label = 'Label',
  Project = 'Project',
  UsersOnWorkspaces = 'UsersOnWorkspaces',
  View = 'View',

  // Team
  Workflow = 'Workflow',
  Issue = 'Issue',
  IssueHistory = 'IssueHistory',
  IssueComment = 'IssueComment',

  // Swarm: the in-flight work signal (one record per agent; the
  // board's live chip and the swarm panel's real-time state).
  SwarmActivity = 'SwarmActivity',
}

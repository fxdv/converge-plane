// The models the client syncs. v1 scope (docs/spec 09/11): the issue
// loop, workflow, labels, members, and saved views. Models for forked
// features outside the active release (AI, actions, integrations,
// support/CRM, projects, cycles, notifications, ...) were removed in the
// M3 dead-feature pass — the server still tolerates unknown model names
// in sync requests, but the client only asks for what it ships.
export enum MODELS {
  Workspace = 'Workspace',
  Team = 'Team',
  Label = 'Label',
  UsersOnWorkspaces = 'UsersOnWorkspaces',
  View = 'View',

  // Team
  Workflow = 'Workflow',
  Issue = 'Issue',
  IssueHistory = 'IssueHistory',
  IssueComment = 'IssueComment',
}

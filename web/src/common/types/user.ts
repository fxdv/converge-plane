import type { Role } from '@converge/types';

interface Workspace {
  name: string;
  slug: string;
  icon: string;
  status?: string;
  id: string;
  actionsEnabled: boolean;
}

export interface Invite {
  id: string;
  workspaceId: string;
  workspace: Workspace;
  status: 'ACCEPTED' | 'DECLINED' | 'INVITED';
}

export interface User {
  fullname: string;
  email: string;
  id: string;
  username: string;
  workspaces: Workspace[];
  invites: Invite[];
  role: Role;
  image?: string;
}

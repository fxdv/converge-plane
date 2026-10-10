import { Action } from '../action';
import { ActionEvent } from '../action-event';
import { Attachment } from '../attachment';
import { Label } from '../label';
import { Project } from '../project';
import { SyncAction } from '../sync-action';
import { Team } from '../team';
import { UsersOnWorkspaces } from '../users-on-workspaces';
import { View } from '../view';

export class Workspace {
  id: string;
  createdAt: Date;
  updatedAt: Date;
  deleted: Date | null;
  name: string;
  slug: string;
  icon: string | null;
  actionsEnabled: boolean;
  preferences: any | null;
  usersOnWorkspaces?: UsersOnWorkspaces[];
  team?: Team[];
  label?: Label[];
  syncAction?: SyncAction[];
  attachments?: Attachment[] | null;
  View?: View[];
  action?: Action[] | null;
  projects?: Project[] | null;
  ActionEvent?: ActionEvent[] | null;
}

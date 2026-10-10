import { Attachment } from '../attachment';
import { Role } from '../invite';
import { Issue } from '../issue';
import { UsersOnWorkspaces } from '../users-on-workspaces';

export class User {
  id: string;
  createdAt: Date;
  updatedAt: Date;
  email: string;
  fullname: string | null;
  username: string;
  initialSetupComplete: boolean;
  anonymousDataCollection: boolean;
  usersOnWorkspaces?: UsersOnWorkspaces[];
  createdBy?: Issue[];
  attachment?: Attachment[];
}

export class PublicUser {
  id: string;
  username: string;
  fullname: string;
  email: string;
  role: Role;
}

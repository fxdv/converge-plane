import type { User } from 'common/types';
import type { IssueSourceMetadataType } from 'common/types';
import { SYSTEM_ACTOR_NAME } from 'common/user-util';

export function getUserDetails(
  sourceMetadata: IssueSourceMetadataType,
  user?: User,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
): any {
  const name = sourceMetadata?.userDisplayName
    ? user?.fullname
      ? `${sourceMetadata.userDisplayName} (${user.fullname})`
      : sourceMetadata.userDisplayName
    : user?.fullname;

  return {
    fullname: name,
    username: user?.username,
  };
}

export function systemUserDetails() {
  return { fullname: SYSTEM_ACTOR_NAME, username: 'converge' };
}

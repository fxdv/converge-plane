import type {
  Issue,
  IssueRequestParamsDto,
  TeamRequestParamsDto,
} from '@converge/types';

import axios from 'axios';

export type MoveIssueToTeamParams = IssueRequestParamsDto &
  TeamRequestParamsDto;

export async function moveIssueToTeam({
  issueId,
  teamId,
}: MoveIssueToTeamParams): Promise<Issue> {
  const response = await axios.post(`/api/v1/issues/${issueId}/move`, {
    teamId,
  });

  return response.data;
}

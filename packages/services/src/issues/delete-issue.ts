import type {
  Issue,
  IssueRequestParamsDto,
  TeamRequestParamsDto,
} from '@converge/types';

import axios from 'axios';

export type DeleteIssueParams = IssueRequestParamsDto & TeamRequestParamsDto;

export async function deleteIssue({
  issueId,
  teamId,
}: DeleteIssueParams): Promise<Issue> {
  const response = await axios.delete(
    `/api/v1/issues/${issueId}?teamId=${teamId}`,
  );

  return response.data;
}

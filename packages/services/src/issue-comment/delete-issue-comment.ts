import type { Issue, IssueCommentRequestParamsDto } from '@converge/types';

import axios from 'axios';

export async function deleteIssueComment({
  issueCommentId,
}: IssueCommentRequestParamsDto): Promise<Issue> {
  const response = await axios.delete(
    `/api/v1/issue_comments/${issueCommentId}`,
  );

  return response.data;
}

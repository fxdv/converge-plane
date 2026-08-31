import { IssueComment, IssueCommentDto } from '@converge/types';
import axios from 'axios';

export async function getIssueCommentReplies({
  issueCommentId,
}: IssueCommentDto): Promise<IssueComment[]> {
  const response = await axios.get(
    `/api/v1/issue_comments/${issueCommentId}/replies`,
  );

  return response.data;
}

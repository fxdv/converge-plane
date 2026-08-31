import { IssueComment, IssueCommentRequestParamsDto } from '@converge/types';
import axios from 'axios';

export async function getIssueComment({
  issueCommentId,
}: IssueCommentRequestParamsDto): Promise<IssueComment> {
  const response = await axios.get(`/api/v1/issue_comments/${issueCommentId}`);

  return response.data;
}

import { Issue } from '@converge/types';
import { SearchDto } from '@converge/types';
import axios from 'axios';

export async function search({
  query,
  workspaceId,
  limit = 10,
}: SearchDto): Promise<Issue[]> {
  const response = await axios.get(
    `/api/v1/search?query=${query}&limit=${limit}&workspaceId=${workspaceId}`,
  );

  return response.data;
}

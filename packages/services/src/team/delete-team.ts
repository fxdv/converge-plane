import { TeamRequestParamsDto } from '@converge/types';
import axios from 'axios';

export async function deleteTeam({ teamId }: TeamRequestParamsDto) {
  const response = await axios.delete(`/api/v1/teams/${teamId}`);

  return response.data;
}

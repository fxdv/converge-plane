import type { Project } from '@converge/types';

import axios from 'axios';

export async function deleteProjectMilestone(
  projectMilestoneId: string,
): Promise<Project> {
  const response = await axios.delete(
    `/api/v1/projects/milestone/${projectMilestoneId}`,
  );

  return response.data;
}

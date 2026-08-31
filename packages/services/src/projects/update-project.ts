import type { UpdateProjectDto, Project } from '@converge/types';

import axios from 'axios';

interface UpdateProjectWithProjectDto extends UpdateProjectDto {
  projectId: string;
}

export async function updateProject({
  projectId,
  ...updateProjectDto
}: UpdateProjectWithProjectDto): Promise<Project> {
  const response = await axios.post(
    `/api/v1/projects/${projectId}`,
    updateProjectDto,
  );

  return response.data;
}

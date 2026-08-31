import type { CreateProjectDto, Project } from '@converge/types';

import axios from 'axios';

export async function createProject(
  createProjectDto: CreateProjectDto,
): Promise<Project> {
  const response = await axios.post(`/api/v1/projects`, createProjectDto);

  return response.data;
}

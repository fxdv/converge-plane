import type { CreateActionDto } from '@converge/types';

import axios from 'axios';

export async function createAction(createActionDto: CreateActionDto) {
  const response = await axios.post(
    `/api/v1/action/create-action`,
    createActionDto,
  );

  return response.data;
}

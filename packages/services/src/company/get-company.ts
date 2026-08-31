import { Company } from '@converge/types';
import axios from 'axios';

export async function getCompaniesByWorkspace(): Promise<Company[]> {
  const response = await axios.get(`/api/v1/company`);

  return response.data;
}

import { IsNumber, IsObject, IsString } from 'class-validator';

import { FilterKey, FilterValue } from '../view';

export class GetIssuesByFilterDTO {
  @IsObject()
  filters: Partial<Record<FilterKey, FilterValue>>;

  @IsString()
  workspaceId: string;
}

export class GetIssuesByNumberDTO {
  @IsNumber()
  number: number;
}

import { Type } from 'class-transformer';
import { IsOptional, IsString, ValidateNested } from 'class-validator';

import { UpdateTeamPreferencesDto } from './update-team-preferences.dto';

export class CreateTeamDto {
  @IsString()
  name: string;

  @IsString()
  identifier: string;

  // The settings form renders inside one workspace, but the team
  // mutation endpoint does not imply it; the id is sent explicitly so
  // multi-workspace users always target the right tenant.
  @IsOptional()
  @IsString()
  workspaceId?: string;

  @IsOptional()
  @IsString()
  icon?: string;

  @IsOptional()
  @ValidateNested()
  @Type(() => UpdateTeamPreferencesDto)
  preferences?: UpdateTeamPreferencesDto;
}

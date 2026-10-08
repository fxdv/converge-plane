import { IsOptional, IsString } from 'class-validator';

export class UpdateTemplateDto {
  @IsOptional()
  @IsString()
  name?: string;

  // TODO: Manoj change this when you have finalised the issue thing

  templateData: Record<string, any>;
}

import { IsOptional, IsString } from 'class-validator';

export class UpdateIssueCommentDto {
  @IsString()
  @IsOptional()
  body?: string;

  @IsString()
  @IsOptional()
  bodyMarkdown?: string;

  @IsOptional()
  @IsString()
  parentId?: string;

  @IsOptional()
  linkCommentMetadata?: any;

  @IsOptional()
  sourceMetadata?: any;
}

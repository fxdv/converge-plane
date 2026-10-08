import { IsOptional, IsString } from 'class-validator';

export class CreateIssueCommentDto {
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

export class CreateIssueCommentRequestParamsDto {
  @IsString()
  issueId: string;
}

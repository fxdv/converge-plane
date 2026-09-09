export enum IssueRelationEnum {
  BLOCKS = 'BLOCKS',
  BLOCKED = 'BLOCKED',
  RELATED = 'RELATED',
  DUPLICATE = 'DUPLICATE',
  DUPLICATE_OF = 'DUPLICATE_OF',
  PARENT = 'PARENT',
  SUB_ISSUE = 'SUB_ISSUE',
  SIMILAR = 'SIMILAR',
}

export const IssueRelationEnumType = {
  BLOCKS: 'BLOCKS',
  BLOCKED: 'BLOCKED',
  RELATED: 'RELATED',
  DUPLICATE: 'DUPLICATE',
  DUPLICATE_OF: 'DUPLICATE_OF',
  PARENT: 'PARENT',
  SUB_ISSUE: 'SUB_ISSUE',
  SIMILAR: 'SIMILAR',
};

export type IssueRelationEnumType =
  (typeof IssueRelationEnumType)[keyof typeof IssueRelationEnumType];

export interface IssueRelationType {
  id: string;
  createdAt: string;
  updatedAt: string;

  issueId: string;
  // null is possible: the denormalized array on the Issue record
  // serializes a NULL created_by as null (the standalone record
  // serializes it as the empty string).
  createdById: string | null;
  relatedIssueId: string;
  // The known vocabulary is IssueRelationEnum, but the wire is a
  // plain string: the render-total policy (a future type from a newer
  // server degrades to a fallback, never crashes the models).
  type: string;
}

import type { UseFormReturn } from 'react-hook-form';

import { z } from 'zod';

export const NewIssueSchema = z.object({
  issues: z.array(
    z.object({
      description: z.optional(z.string()),
      title: z.string(),
      stateId: z.string(),
      labelIds: z.array(z.string()),
      priority: z.number(),
      assigneeId: z.optional(z.string()),
      parentId: z.optional(z.string()),
      teamId: z.optional(z.string()),
      projectId: z.optional(z.string()),
      projectMilestoneId: z.optional(z.string()),
    }),
  ),
});

export const NewIssueTemplateSchema = z.object({
  issues: z.array(
    z.object({
      description: z.optional(z.string()),
      title: z.optional(z.string()),

      stateId: z.string(),

      labelIds: z.array(z.string()),
      priority: z.number(),
      assigneeId: z.optional(z.string()),
      parentId: z.optional(z.string()),
      teamId: z.optional(z.string()),
      projectId: z.optional(z.string()),
      projectMilestoneId: z.optional(z.string()),
    }),
  ),
});

export const draftKey = 'CreateIssueDraft';

export type NewIssueFormValues = z.infer<typeof NewIssueSchema>;

// Zod 4 keeps a schema's input and output apart, so a typed form no longer
// fits the default UseFormReturn. These views address fields by string.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type IssueDraftForm = UseFormReturn<any>;

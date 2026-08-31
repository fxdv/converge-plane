import type React from 'react';

import { cn } from '@tegonhq/ui/lib/utils';
import { observer } from 'mobx-react-lite';

import { DueDate } from 'modules/issues/components';

import { useIssueData } from 'hooks/issues';
import { useUpdateIssueMutation } from 'services/issues';

export const EngineeringProperties = observer(() => {
  const issue = useIssueData();
  const { mutate: updateIssue } = useUpdateIssueMutation({});

  const dueDateChange = (dueDate: Date) => {
    updateIssue({
      id: issue.id,
      dueDate: dueDate ? dueDate.toISOString() : undefined,
      teamId: issue.teamId,
    });
  };

  return (
    <>
      <div className={cn('flex flex-col items-start')}>
        <div className="text-xs text-left">Due Date</div>
        <DueDate dueDate={issue.dueDate} dueDateChange={dueDateChange} />
      </div>
    </>
  );
});

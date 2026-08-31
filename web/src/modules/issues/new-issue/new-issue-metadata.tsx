import { FormField, FormItem, FormControl } from '@converge/ui/components/form';
import { observer } from 'mobx-react-lite';
import React from 'react';
import { type UseFormReturn } from 'react-hook-form';

import type { TeamType } from 'common/types';

import {
  IssueAssigneeDropdown,
  IssueLabelDropdown,
  IssuePriorityDropdown,
  IssueStatusDropdown,
} from '../components';
interface NewIssueMetadataProps {
  form: UseFormReturn;
  team: TeamType;
  index: number;
}

export const NewIssueMetadata = observer(
  ({ form, team, index }: NewIssueMetadataProps) => {
    function inputName(name: string) {
      return `issues.${index}.${name}`;
    }

    return (
      <>
        <FormField
          control={form.control}
          name={inputName('stateId')}
          render={({ field }) => (
            <FormItem>
              <FormControl>
                <IssueStatusDropdown
                  onChange={field.onChange}
                  value={field.value}
                  teamIdentifier={team.identifier}
                />
              </FormControl>
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name={inputName('labelIds')}
          render={({ field }) => (
            <FormItem>
              <FormControl>
                <IssueLabelDropdown
                  value={field.value}
                  onChange={field.onChange}
                  teamIdentifier={team.identifier}
                />
              </FormControl>
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name={inputName('assigneeId')}
          render={({ field }) => (
            <FormItem>
              <FormControl>
                <IssueAssigneeDropdown
                  value={field.value}
                  teamId={team.id}
                  onChange={field.onChange}
                />
              </FormControl>
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name={inputName('priority')}
          render={({ field }) => (
            <FormItem>
              <FormControl className="max-w-[200px]">
                <IssuePriorityDropdown
                  value={field.value}
                  onChange={field.onChange}
                />
              </FormControl>
            </FormItem>
          )}
        />
      </>
    );
  },
);

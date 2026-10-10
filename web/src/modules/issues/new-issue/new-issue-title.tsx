import { AdjustableTextArea } from '@converge/ui/components/adjustable-textarea';
import {
  FormControl,
  FormField,
  FormItem,
  FormMessage,
} from '@converge/ui/components/form';

import { type IssueDraftForm } from './new-issues-type';

interface NewIssueTitleProps {
  form: IssueDraftForm;
  index: number;
  isSubIssue: boolean;
}

export function NewIssueTitle({ form, index, isSubIssue }: NewIssueTitleProps) {
  return (
    <div className="p-4 py-2 text-lg font-normal w-full gap-2 flex items-center">
      <FormField
        control={form.control}
        name={`issues.${index}.title`}
        render={({ field }) => (
          <FormItem className="grow overflow-hidden">
            <FormControl>
              <div className="flex items-center gap-1">
                <AdjustableTextArea
                  className="border-0 py-0 resize-none bg-transparent no-scrollbar overflow-hidden outline-hidden focus-visible:ring-0 w-fit"
                  placeholderClassName="font-medium"
                  placeholder="Issue title"
                  autoFocus={index === 0 && !isSubIssue}
                  {...field}
                />
              </div>
            </FormControl>

            <FormMessage />
          </FormItem>
        )}
      />
    </div>
  );
}

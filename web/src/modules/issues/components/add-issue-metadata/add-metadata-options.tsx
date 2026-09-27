import { CommandGroup, CommandItem } from '@converge/ui/components/command';
import * as React from 'react';

import { allCommands } from './add-issue-metadata-interface';
import {
  IssueAssigneeDropdownWithoutContext,
  IssueLabelDropdownWithoutContext,
} from '../issue-metadata';

export function DefaultPopoverContent({
  onSelect,
  hideCommands = [],
}: {
  onSelect: (command: CommandInterface) => void;
  hideCommands?: string[];
}) {
  const getCommands = () => {
    if (hideCommands && hideCommands.length > 0) {
      return allCommands.filter(
        (command) => !hideCommands.includes(command.id),
      );
    }

    return allCommands;
  };

  return (
    <CommandGroup>
      {getCommands().map((command) => (
        <CommandItem
          key={command.name}
          value={command.name}
          className="flex items-center"
          onSelect={() =>
            onSelect({ name: command.name as KeyType, id: command.id })
          }
        >
          <command.Icon size={16} className="mr-2" />
          {command.name}
        </CommandItem>
      ))}
    </CommandGroup>
  );
}

export type KeyType = 'Assignee' | 'Label';

interface MetadataContentProps {
  value?: string | string[];
  input?: string;
  teamIdentifier?: string;
  onChange?: (value: string | string[]) => void;
  onClose: () => void;
}

export const ContentMap: Record<
  KeyType,
  React.ComponentType<MetadataContentProps>
> = {
  Assignee: IssueAssigneeDropdownWithoutContext,
  Label: IssueLabelDropdownWithoutContext,
};

export interface CommandInterface {
  name: KeyType;
  id: string;
}

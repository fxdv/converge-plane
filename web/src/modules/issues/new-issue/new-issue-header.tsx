import { observer } from 'mobx-react-lite';

import type { IssueType, TeamType } from 'common/types';

import { TeamDropdown } from './team-dropdown';

interface NewIssueHeaderProps {
  team: TeamType;
  setTeam: (value: string) => void;
  resetFormValues: (defaultValues: Partial<IssueType>) => void;
}

export const NewIssueHeader = observer(
  ({ team, setTeam }: NewIssueHeaderProps) => {
    return (
      <div className="flex p-4 pb-0 gap-2 items-center">
        <TeamDropdown
          value={team.identifier}
          onChange={(value: string) => setTeam(value)}
        />
      </div>
    );
  },
);

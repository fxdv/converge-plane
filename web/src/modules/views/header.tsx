import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
} from '@converge/ui/components/breadcrumb';
import { Button } from '@converge/ui/components/button';
import { TeamIcon } from '@converge/ui/components/team-icon';
import { observer } from 'mobx-react-lite';
import Link from 'next/link';
import { useRouter } from 'next/router';
import * as React from 'react';

import { HeaderLayout } from 'common/header-layout';

import { useCurrentTeam } from 'hooks/teams';

import { NewViewDialog } from './new-view-dialog';

interface HeaderProps {
  title: string;
}

export const Header = observer(({ title }: HeaderProps) => {
  const team = useCurrentTeam();
  const [open, setOpen] = React.useState(false);

  const {
    query: { workspaceSlug },
  } = useRouter();

  const actions = (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        New view
      </Button>
      <NewViewDialog open={open} setOpen={setOpen} />
    </>
  );

  return (
    <HeaderLayout actions={actions}>
      <Breadcrumb>
        {team && (
          <BreadcrumbItem>
            <BreadcrumbLink
              as={Link}
              className="flex items-center gap-2 font-medium"
              href={`/${workspaceSlug}/team/${team.identifier}/all`}
            >
              <TeamIcon preferences={team.preferences} name={team.name} />

              <span className="inline-block">{team.name}</span>
            </BreadcrumbLink>
          </BreadcrumbItem>
        )}
        <BreadcrumbItem>
          <BreadcrumbLink>{title}</BreadcrumbLink>
        </BreadcrumbItem>
      </Breadcrumb>
    </HeaderLayout>
  );
});

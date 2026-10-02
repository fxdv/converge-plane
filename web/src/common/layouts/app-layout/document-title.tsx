import { observer } from 'mobx-react-lite';
import { useRouter } from 'next/router';
import React from 'react';

import { useCurrentTeam } from 'hooks/teams';

const SECTION: Record<string, string> = {
  '/inbox': 'Inbox',
  '/swarm': 'Swarm',
  '/metrics': 'Metrics',
  '/my-issues': 'My issues',
  '/views': 'Views',
  '/teams': 'Teams',
  '/settings': 'Settings',
  '/auth': 'Sign in',
};

// The document title was empty on every route. Name the page from the
// issue identifier, the team, or the section.
export const DocumentTitle = observer(() => {
  const router = useRouter();
  const team = useCurrentTeam();
  const { issueId, settingsSection } = router.query;

  React.useEffect(() => {
    const bits: string[] = [];
    if (typeof issueId === 'string' && issueId) {
      bits.push(issueId);
    } else if (team?.name) {
      bits.push(team.name);
    } else if (typeof settingsSection === 'string' && settingsSection) {
      bits.push(settingsSection.charAt(0).toUpperCase() + settingsSection.slice(1));
    } else {
      const section = Object.entries(SECTION).find(([path]) =>
        router.pathname.includes(path),
      );
      if (section) {
        bits.push(section[1]);
      }
    }
    bits.push('Converge');
    document.title = bits.join(' · ');
  }, [issueId, settingsSection, team?.name, router.pathname]);

  return null;
});

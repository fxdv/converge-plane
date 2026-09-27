import { Loader } from '@converge/ui/components/loader';
import * as React from 'react';

import { hash } from 'common/common-utils';

import { useCurrentWorkspace } from 'hooks/workspace';

import { initDatabase } from 'store/database';
import { UserContext } from 'store/user-context';

interface Props {
  children: React.ReactElement;
}

export function DatabaseWrapper(props: Props): React.ReactElement {
  const { children } = props;
  const workspace = useCurrentWorkspace();
  const user = React.useContext(UserContext);
  const [loading, setLoading] = React.useState(true);

  // The router's query is empty on the first render after a full page
  // load, so the workspace can resolve a render late.
  React.useEffect(() => {
    if (workspace) {
      const version = localStorage.getItem('version');
      if (version !== process.env.NEXT_PUBLIC_VERSION) {
        localStorage.setItem('version', process.env.NEXT_PUBLIC_VERSION);
      }

      initDatabase(hash(`${workspace.id}__${user.id}`));
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace]);

  if (loading) {
    return <Loader text="Starting database..." />;
  }

  return <>{children}</>;
}

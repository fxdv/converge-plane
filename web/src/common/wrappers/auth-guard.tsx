import { Loader } from '@converge/ui/components/loader';
import { useRouter } from 'next/router';
import React, { cloneElement } from 'react';
import { Session } from 'common/auth';

interface Props {
  children: React.ReactElement;
}

export function AuthGuard(props: Props): React.ReactElement {
  const { children } = props;
  const router = useRouter();
  const [isLoading, setLoading] = React.useState(true);

  React.useEffect(() => {
    checkForSession();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function checkForSession() {
    try {
      const exists = await Promise.race([
        Session.doesSessionExist(),
        new Promise<boolean>((resolve) => {
          setTimeout(() => resolve(false), 8000);
        }),
      ]);
      if (exists) {
        router.replace('/');
      } else {
        setLoading(false);
      }
    } catch {
      setLoading(false);
    }
  }

  if (!isLoading) {
    return cloneElement(children);
  }

  return <Loader />;
}

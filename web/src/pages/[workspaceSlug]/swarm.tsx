import { useRouter } from 'next/router';
import React, { useEffect } from 'react';

/** Client navigations to the legacy path land here; HTTP uses next.config redirect. */
export default function SwarmLegacyRedirect(): React.ReactElement | null {
  const router = useRouter();

  useEffect(() => {
    if (!router.isReady) {
      return;
    }
    const slug = router.query.workspaceSlug;
    if (typeof slug !== 'string' || !slug) {
      return;
    }
    void router.replace(`/${slug}/floor`);
  }, [router.isReady, router.query.workspaceSlug]);

  return null;
}

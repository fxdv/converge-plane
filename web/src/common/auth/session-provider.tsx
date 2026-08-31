import React from 'react';

import { Session } from './session';

interface SessionAuthProps {
  children: React.ReactElement;
  onSessionExpired?: () => void;
}

/**
 * Client-side session provider (R-5):
 * re-renders children when the session changes and forwards
 * session-expired events. Session state itself lives in cookies, so no
 * provider plumbing is needed beyond the listeners.
 */
export function SessionAuth({
  children,
  onSessionExpired,
}: SessionAuthProps): React.ReactElement {
  const [, force] = React.useReducer((x: number) => x + 1, 0);

  React.useEffect(() => {
    const offUpdate = Session.onSessionUpdate(() => force());
    const offExpired = Session.onSessionExpired(() => onSessionExpired?.());
    return () => {
      offUpdate();
      offExpired();
    };
  }, [onSessionExpired]);

  return children;
}

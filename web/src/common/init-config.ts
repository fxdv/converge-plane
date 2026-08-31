import { Session } from 'common/auth';

// Initialise the client-side session (cookie parsing + renewal
// maintenance). Runs once per page load, before the app tree mounts.
export function initSession() {
  if (typeof window !== 'undefined') {
    Session.start();
  }
}

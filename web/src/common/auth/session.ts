/**
 * Minimal same-origin session client for the Converge API.
 *
 * Speaks the wire protocol implemented by server/internal/auth
 * (session.go): base64-encoded JSON cookies where the access/refresh
 * tokens are HttpOnly and the front token, anti-csrf token and
 * last-access marker are readable from JavaScript. The client keeps
 * session state entirely in those cookies and only ever calls
 * POST /api/auth/session/refresh and POST /api/auth/signout; tokens are
 * parsed with atob() + JSON.parse, so values must be standard base64.
 *
 * Replaces the previous third-party session SDK (R-5).
 */

export interface SessionInfo {
  userId: string;
  /** Access-token expiry, ms epoch. */
  expires: number;
}

type UpdateListener = (session: SessionInfo | undefined) => void;

const FRONT_TOKEN_COOKIE = 'sFrontToken';
const ANTI_CSRF_COOKIE = 'sAntiCsrf';
const API_BASE = '/api/auth';

/** The JS-readable session token: {r, ate, uid} as base64 JSON. */
interface FrontToken {
  r: number;
  ate: number;
  uid: string;
}

const FIVE_MIN = 5 * 60 * 1000;
const THIRTY_MIN = 30 * 60 * 1000;

function getCookie(name: string): string | undefined {
  const row = document.cookie
    .split(';')
    .map((c) => c.trim())
    .find((c) => c.startsWith(`${name}=`));
  return row ? decodeURIComponent(row.slice(name.length + 1)) : undefined;
}

function decodeFrontToken(): FrontToken | undefined {
  const raw = getCookie(FRONT_TOKEN_COOKIE);
  if (!raw) {
    return undefined;
  }
  try {
    const parsed = JSON.parse(atob(raw)) as FrontToken;
    if (!parsed.ate || !parsed.uid) {
      return undefined;
    }
    return parsed;
  } catch {
    return undefined;
  }
}

class SessionClient {
  private updateListeners = new Set<UpdateListener>();
  private expiredListeners = new Set<() => void>();
  private renewTimer: ReturnType<typeof setInterval> | undefined;
  private started = false;

  /** The live session, or undefined when the access token is gone. */
  get info(): SessionInfo | undefined {
    const token = decodeFrontToken();
    if (!token || token.ate < Date.now()) {
      return undefined;
    }
    return { userId: token.uid, expires: token.ate };
  }

  /**
   * True when a session is live, or can be made live with a silent
   * refresh (access token expired, refresh cookie still valid). This
   * gives "remember me" behaviour: an idle user who returns within the
   * refresh-token window is transparently signed back in.
   */
  async doesSessionExist(): Promise<boolean> {
    if (this.info) {
      return true;
    }
    return (await this.renewSession()) !== undefined;
  }

  /**
   * Re-issue the session material. The server validates the (HttpOnly)
   * refresh cookie and the anti-csrf cookie, then rotates every cookie;
   * resolves with the new session, or undefined when there is none.
   *
   * When `silent` is true a 401 is reported without firing
   * session-expired, for the app-load probe of a logged-out user.
   */
  async renewSession(silent = false): Promise<SessionInfo | undefined> {
    const headers: Record<string, string> = {};
    const antiCsrf = getCookie(ANTI_CSRF_COOKIE);
    if (antiCsrf) {
      headers['anti-csrf'] = antiCsrf;
    }
    try {
      const res = await fetch(`${API_BASE}/session/refresh`, {
        method: 'POST',
        credentials: 'same-origin',
        headers,
      });
      if (!res.ok) {
        if (!silent) {
          this.fireExpired();
        }
        return undefined;
      }
      // The response Set-Cookie header is already applied by the browser;
      // re-read the (new) front token to learn the fresh expiry.
      const session = this.info;
      this.fireUpdate(session);
      this.scheduleRenew();
      return session;
    } catch {
      // Network failure: keep whatever session state we have.
      return this.info;
    }
  }

  /** Revoke the session (server expires every session cookie). */
  async signOut(): Promise<void> {
    try {
      await fetch(`${API_BASE}/signout`, {
        method: 'POST',
        credentials: 'same-origin',
      });
    } catch {
      // Even if the call fails, drop the client-visible state below.
    }
    this.stop();
    this.fireExpired();
  }

  /** Subscribe to session created/renewed/expired; returns an unsubscribe. */
  onSessionUpdate(listener: UpdateListener): () => void {
    this.updateListeners.add(listener);
    return () => this.updateListeners.delete(listener);
  }

  /** Subscribe to session-expired; returns an unsubscribe. */
  onSessionExpired(listener: () => void): () => void {
    this.expiredListeners.add(listener);
    return () => this.expiredListeners.delete(listener);
  }

  /**
   * Start renewal maintenance. Idempotent; call once on load. Keeps the
   * hour-long access token alive while the app is open and restores the
   * session after idle via a silent startup probe.
   */
  start(): void {
    if (typeof window === 'undefined' || this.started) {
      return;
    }
    this.started = true;
    window.addEventListener('focus', this.onFocus);
    this.scheduleRenew();
    if (!this.info) {
      // Access token absent/expired: probe once (silently) in case the
      // refresh cookie is still valid. No timer is set while logged out.
      void this.renewSession(true);
    }
  }

  private readonly onFocus = () => {
    if (this.info) {
      void this.renewSession();
    }
  };

  private scheduleRenew(): void {
    if (this.renewTimer) {
      clearInterval(this.renewTimer);
      this.renewTimer = undefined;
    }
    // Nothing to renew without a live session.
    if (!this.info) {
      return;
    }
    // Access tokens live an hour server-side; renew at (expiry - 5min),
    // clamped to a 5-30min window so long sessions never call the API
    // with an expired access token.
    const remaining = Math.max(
      FIVE_MIN,
      this.info.expires - Date.now() - FIVE_MIN,
    );
    const interval = Math.min(remaining, THIRTY_MIN);
    this.renewTimer = setInterval(() => {
      if (this.info) {
        void this.renewSession();
      }
    }, interval);
  }

  private stop(): void {
    if (this.renewTimer) {
      clearInterval(this.renewTimer);
      this.renewTimer = undefined;
    }
  }

  private fireUpdate(session: SessionInfo | undefined): void {
    this.updateListeners.forEach((listener) => listener(session));
  }

  private fireExpired(): void {
    this.stop();
    this.expiredListeners.forEach((listener) => listener());
  }
}

export const Session = new SessionClient();

/** Convenience alias so existing `signOut` call sites stay unchanged. */
export const signOut = (): Promise<void> => Session.signOut();

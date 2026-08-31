/**
 * Magic-link (passwordless) sign-in client for the Converge API.
 *
 * Replaces the previous third-party passwordless SDK surface:
 * createCode / consumeCode / clearLoginAttemptInfo, talking to the
 * same-origin /api/auth endpoints (proxied to the Go API).
 */

const API_BASE = '/api/auth';
const LOGIN_ATTEMPT_KEY = 'converge.preAuthSessionId';

export interface CreateCodeResponse {
  status: string;
  /** Present when status is not OK; user-friendly rejection reason. */
  reason?: string;
  preAuthSessionId?: string;
  deviceId?: string;
  /** Only in API dev mode: the magic link, so local setups without an
   * email provider can offer it directly. */
  devMagicLink?: string;
}

export interface ConsumeCodeUser {
  id: string;
  emails: string[];
  loginMethods: Array<{
    id: string;
    emailId: string;
    linkScore: number;
  }>;
}

export interface ConsumeCodeResponse {
  /** "OK" | "INVALID_LINK_CODE" | "EXPIRED_LINK_CODE" | ... */
  status: string;
  user?: ConsumeCodeUser;
  createdNewRecipeUser?: boolean;
}

/** Error thrown for non-2xx responses. */
export class AuthApiError extends Error {
  isAuthApiError = true;
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = (await res.json().catch(() => ({}))) as T & {
    message?: string;
  };
  if (!res.ok) {
    throw new AuthApiError(data.message || `request to ${path} failed`);
  }
  return data;
}

/**
 * Ask the API to create a magic-link code for `email` (sign-in/up:
 * unknown emails are not an error at this step; the account
 * materializes when the code is consumed).
 */
export async function createCode({
  email,
}: {
  email: string;
}): Promise<CreateCodeResponse> {
  const data = await postJSON<CreateCodeResponse>('/signinup/code', { email });
  if (data.preAuthSessionId) {
    // Kept so a reload of the verify page (hash lost to history
    // handling) can still complete the sign-in.
    sessionStorage.setItem(LOGIN_ATTEMPT_KEY, data.preAuthSessionId);
  }
  return data;
}

/**
 * Complete the sign-in from the magic link. The link has the shape
 * `<origin>/auth/verify?preAuthSessionId=..#<CODE>`: the pre-auth id
 * comes from the query string and the link code from the URL hash
 * fragment.
 */
export async function consumeCode(): Promise<ConsumeCodeResponse> {
  const url = new URL(window.location.href);
  const preAuthSessionId =
    url.searchParams.get('preAuthSessionId') ||
    sessionStorage.getItem(LOGIN_ATTEMPT_KEY) ||
    '';
  const linkCode = url.hash.replace(/^#/, '');
  return postJSON<ConsumeCodeResponse>('/signinup/code/consume', {
    linkCode,
    preAuthSessionId,
  });
}

/** Drop the stored login attempt (call after sign-in completes). */
export function clearLoginAttemptInfo(): void {
  sessionStorage.removeItem(LOGIN_ATTEMPT_KEY);
}

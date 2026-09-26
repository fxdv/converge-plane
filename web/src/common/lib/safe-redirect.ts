// Post-sign-in redirect targets arrive in the query string, so they are
// attacker-controlled: a link to /auth/verify?redirectToPath=https://evil
// would hand a freshly signed-in user to another site. Only same-origin
// absolute paths are honoured.

const FALLBACK = '/';

export function safeRedirectPath(raw: unknown, fallback = FALLBACK): string {
  if (typeof raw !== 'string' || raw === '') {
    return fallback;
  }
  // "//host" and "/\host" are protocol-relative in browsers; control
  // characters are stripped by URL parsers and can smuggle either form.
  if (!raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) {
    return fallback;
  }
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(raw)) {
    return fallback;
  }
  try {
    const base = 'http://converge.invalid';
    const url = new URL(raw, base);
    if (url.origin !== base) {
      return fallback;
    }
    return url.pathname + url.search + url.hash;
  } catch {
    return fallback;
  }
}

// The Content-Security-Policy the middleware sends (Phase 5, H1). Pure so
// the node test runner can pin it without Next's runtime.

// The origin of the backend the browser talks to directly: the SSE stream
// (socket-data-sync.tsx) opens an EventSource on NEXT_PUBLIC_BACKEND_HOST.
// Everything else rides the same-origin /api proxy. Null when unset or
// unparsable, so the policy falls back to 'self' alone.
export function backendOrigin(host: string | undefined): string | null {
  if (!host) {
    return null;
  }
  try {
    const url = new URL(host);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return null;
    }
    return url.origin;
  } catch {
    return null;
  }
}

export function buildCsp({
  nonce,
  isDev,
  backendHost,
}: {
  nonce: string;
  isDev: boolean;
  backendHost: string | undefined;
}): string {
  const scriptSrc = isDev
    ? `script-src 'self' 'nonce-${nonce}' 'strict-dynamic' 'unsafe-eval'`
    : `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'`;

  const backend = backendOrigin(backendHost);
  // No websockets in the app (realtime is SSE); dev adds ws: for Next's
  // HMR socket only.
  const connectSrc = ["'self'", backend, isDev ? 'ws:' : null]
    .filter(Boolean)
    .join(' ');

  return [
    "default-src 'self'",
    scriptSrc,
    // 'unsafe-inline' stays: Radix (react-remove-scroll), the lightbox and
    // tiptap inject <style> elements, and SSR emits style="" attributes
    // (react-virtualized rows, Radix popper positioning) that a nonce
    // cannot cover.
    "style-src 'self' 'unsafe-inline'",
    // Editor images and files resolve through the same-origin
    // /api/v1/attachment proxy (use-src.ts), whatever src the content
    // carries; data:/blob: cover pending uploads and inline icons. No
    // remote avatars or hotlinked images are rendered.
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    `connect-src ${connectSrc}`,
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "object-src 'none'",
    "form-action 'self'",
  ].join('; ');
}

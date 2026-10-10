import type { NextRequest } from 'next/server';

import { NextResponse } from 'next/server';

import { buildCsp } from 'common/lib/csp';

// Phase 5 (H1): per-request CSP nonces for Next.js scripts.
export function middleware(request: NextRequest) {
  const nonce = Buffer.from(crypto.randomUUID()).toString('base64');
  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-nonce', nonce);

  // NEXT_PUBLIC_* is inlined at build time, as it is for the client
  // bundle that opens the SSE stream: both always name the same backend.
  const csp = buildCsp({
    nonce,
    isDev: process.env.NODE_ENV === 'development',
    backendHost: process.env.NEXT_PUBLIC_BACKEND_HOST,
  });

  // Next.js extracts the nonce from the CSP on the *request* during SSR so
  // script tags get a matching nonce. Response-only CSP blocked hydration on
  // statically optimized pages (e.g. /auth stuck on the loader).
  requestHeaders.set('Content-Security-Policy', csp);

  const response = NextResponse.next({
    request: { headers: requestHeaders },
  });
  response.headers.set('Content-Security-Policy', csp);
  response.headers.set('X-Frame-Options', 'DENY');
  response.headers.set('X-Content-Type-Options', 'nosniff');
  response.headers.set('Referrer-Policy', 'strict-origin-when-cross-origin');
  response.headers.set(
    'Permissions-Policy',
    'camera=(), microphone=(), geolocation=()',
  );
  return response;
}

export const config = {
  matcher: ['/((?!_next/static|_next/image|favicon.ico).*)'],
};

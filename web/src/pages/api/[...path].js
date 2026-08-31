/* eslint-disable import/no-anonymous-default-export */

// Forwards all /api/* calls from the web origin to the Converge API server.
// The web client calls the API same-origin (see common/lib/config.ts), so
// session cookies work without CORS. The full path is preserved: the API
// server owns /api/auth/* and /api/v1/* routes.

import httpProxy from 'http-proxy';
import getConfig from 'next/config';

const { publicRuntimeConfig } = getConfig();

// API_PROXY_TARGET (server-side only) overrides the build-time backend
// host: in the docker compose image the proxy must reach the internal
// `api` service while the browser's SSE stream still targets
// NEXT_PUBLIC_BACKEND_HOST. Unset in local dev, where both are the same.
const API_URL =
  process.env.API_PROXY_TARGET || publicRuntimeConfig.NEXT_PUBLIC_BACKEND_HOST;

const proxy = httpProxy.createProxyServer();

// Skip the body parser so request/response bodies stream through
// untouched (important for large sync payloads and future uploads).
export const config = {
  api: {
    bodyParser: false,
  },
};

export default (req, res) => {
  if (!API_URL) {
    res.statusCode = 502;
    res.end('backend host not configured (NEXT_PUBLIC_BACKEND_HOST)');
    return;
  }

  proxy.once('error', (err) => {
    console.error('[api proxy] error:', err.message);
    if (!res.headersSent) {
      res.statusCode = 502;
      res.end('backend unavailable');
    } else {
      res.end();
    }
  });

  return new Promise((resolve) => {
    res.on('close', resolve);
    proxy.web(req, res, {
      target: API_URL,
      changeOrigin: true,
      // Preserve the original path (including the /api prefix).
      // The SSE stream connects directly to the backend host, not through
      // this proxy, so no upgrade handling is needed.
    });
  });
};

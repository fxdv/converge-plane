/* eslint-disable import/no-anonymous-default-export */

// Forwards all /api/* calls from the web origin to the Circle API server.
// The web client calls the API same-origin (see common/lib/config.ts), so
// session cookies work without CORS. The full path is preserved: the API
// server owns /api/auth/* and /api/v1/* routes.

import httpProxy from 'http-proxy';
import getConfig from 'next/config';

const { publicRuntimeConfig } = getConfig();

const API_URL = publicRuntimeConfig.NEXT_PUBLIC_BACKEND_HOST;

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
      // socket.io connections go directly to the API host, not
      // through this proxy, so no websocket upgrade handling is needed.
    });
  });
};

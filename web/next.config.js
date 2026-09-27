/** Copyright (c) 2024, Tegon, all rights reserved. **/

module.exports = {
  reactStrictMode: false,
  poweredByHeader: false,
  // Overridable output dir (CONVERGE_WEB_DIST_DIR=.next-ci) so a `next build`
  // repro can run without clobbering a live dev server's .next cache.
  distDir: process.env.CONVERGE_WEB_DIST_DIR || '.next',
  transpilePackages: ['geist', '@converge/ui'],
  async redirects() {
    return [
      {
        source: '/',
        destination: '/auth',
        permanent: true,
      },
    ];
  },
  // The /_next/image optimizer fetches and transcodes on request; nothing
  // here needs it, and with it off Next answers the route with a 404.
  images: { unoptimized: true },
  // The browser calls /api same-origin through the proxy route, so no CORS
  // headers are sent: a cross-origin caller gets nothing it can read. The
  // CSP carries only directives that cannot break Next's inline bootstrap
  // scripts; script-src needs nonces and is a separate change.
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          {
            key: 'Content-Security-Policy',
            value:
              "frame-ancestors 'none'; base-uri 'self'; object-src 'none'; form-action 'self'",
          },
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          {
            key: 'Permissions-Policy',
            value: 'camera=(), microphone=(), geolocation=()',
          },
        ],
      },
    ];
  },
  devIndicators: {
    position: 'bottom-right',
  },
  output: 'standalone',
};

// Pin react-day-picker to its ESM entry. The package is dual-format
// (CJS dist/index.js + ESM dist/index.esm.js); depending on the webpack
// compilation, the CJS entry wins, and its require('date-fns') then fails
// because date-fns v3 is ESM-only. Resolving the ESM entry everywhere is
// the standard workaround and keeps the package tree-shakable.
const path = require('path');
const rdpPkg = require.resolve('react-day-picker/package.json', {
  paths: [path.join(__dirname, '../packages/ui')],
});

module.exports = {
  ...module.exports,
  webpack(config) {
    config.resolve.alias['react-day-picker$'] = path.join(
      path.dirname(rdpPkg),
      'dist/index.esm.js',
    );
    return config;
  },
};

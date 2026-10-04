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
      {
        source: '/:workspaceSlug/swarm',
        destination: '/:workspaceSlug/floor',
        permanent: true,
      },
    ];
  },
  // The /_next/image optimizer fetches and transcodes on request; nothing
  // here needs it, and with it off Next answers the route with a 404.
  images: { unoptimized: true },
  // Security headers (incl. CSP nonces) are set in src/middleware.ts.
  devIndicators: {
    position: 'bottom-right',
  },
  output: 'standalone',
  // Otherwise next dev writes AGENTS.md and CLAUDE.md into web/.
  agentRules: false,
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

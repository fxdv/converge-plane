/** Copyright (c) 2024, Tegon, all rights reserved. **/

module.exports = {
  reactStrictMode: false,
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
  async headers() {
    return [
      {
        // matching all API routes
        source: '/api/:path*',
        headers: [
          { key: 'Access-Control-Allow-Credentials', value: 'true' },
          { key: 'Access-Control-Allow-Origin', value: '*' }, // replace this your actual origin
          {
            key: 'Access-Control-Allow-Methods',
            value: 'GET,DELETE,PATCH,POST,PUT',
          },
          {
            key: 'Access-Control-Allow-Headers',
            value:
              'X-CSRF-Token, X-Requested-With, Accept, Accept-Version, Content-Length, Content-MD5, Content-Type, Date, X-Api-Version',
          },
        ],
      },
    ];
  },
  devIndicators: {
    buildActivityPosition: 'bottom-right',
  },
  swcMinify: true,
  publicRuntimeConfig: {
    // Will be available on both server and client
    NEXT_PUBLIC_VERSION: process.env.NEXT_PUBLIC_VERSION,
    NEXT_PUBLIC_NODE_ENV: process.env.NEXT_PUBLIC_NODE_ENV,
    NEXT_PUBLIC_BASE_HOST: process.env.NEXT_PUBLIC_BASE_HOST,
    NEXT_PUBLIC_BACKEND_HOST: process.env.NEXT_PUBLIC_BACKEND_HOST,
    NEXT_PUBLIC_AI_HOST: process.env.NEXT_PUBLIC_AI_HOST,
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
      'dist/index.esm.js'
    );
    return config;
  },
};

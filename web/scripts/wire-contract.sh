#!/usr/bin/env bash
# Wire-contract check (client side) — see
# src/store/__tests__/wire-contract.test.ts.
#
# Compiles the test harness plus the real MST model sources with the
# project's TypeScript, then executes them under node's built-in test
# runner. Zero extra dependencies: runs in CI with Node >= 18 and the
# existing typescript devDep.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

pnpm exec tsc src/store/__tests__/wire-contract.test.ts \
  --outDir "$OUT" \
  --module commonjs --target es2020 \
  --esModuleInterop --skipLibCheck \
  --baseUrl src

# The models are imported through the app's src-relative aliases
# ('store/...', 'common/...'); map them onto the compiled tree at
# runtime. Bare package imports (mobx-state-tree, dexie) resolve via
# NODE_PATH into web/node_modules.
cat > "$OUT/alias-shim.js" <<'EOF'
const Module = require('module');
const path = require('path');
const root = process.env.CONTRACT_OUT;
const orig = Module._resolveFilename;
Module._resolveFilename = function (request, ...args) {
  for (const alias of ['store/', 'common/', 'hooks/', 'services/', 'modules/']) {
    if (request.startsWith(alias)) {
      request = path.join(root, request);
    }
  }
  return orig.call(this, request, ...args);
};
EOF

CONTRACT_OUT="$OUT" NODE_PATH="$(pwd)/node_modules" \
  node -r "$OUT/alias-shim.js" "$OUT/store/__tests__/wire-contract.test.js"

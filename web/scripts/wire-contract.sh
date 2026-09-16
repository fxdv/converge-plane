#!/usr/bin/env bash
# Wire-contract check (client side) — see the *.test.ts files under src/.
#
# Compiles every test file with the project's TypeScript, then executes
# each under node's built-in test runner. Zero extra dependencies: runs in
# CI with Node >= 18 and the existing typescript devDep.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

TESTS="$(find src -name '*.test.ts' | sort)"
if [ -z "$TESTS" ]; then
  echo "wire-contract: no *.test.ts files found under src/" >&2
  exit 1
fi

# --rootDir pins the compiled tree at $OUT/src/... no matter how many
# test files exist, so the alias shim below maps app aliases onto it.
pnpm exec tsc $TESTS \
  --outDir "$OUT" \
  --rootDir src \
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

FAIL=0
for t in $TESTS; do
  rel="${t#src/}"
  echo "--- $t"
  CONTRACT_OUT="$OUT" NODE_PATH="$(pwd)/node_modules" \
    node -r "$OUT/alias-shim.js" "$OUT/${rel%.ts}.js" || FAIL=1
done

if [ "$FAIL" -ne 0 ]; then
  echo "wire-contract: one or more suites failed" >&2
  exit 1
fi

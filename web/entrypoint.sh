#!/bin/sh
set -e

# Substitute PROD_ placeholders with runtime values baked in at build time.
makeSedCommands() {
  printenv | \
    grep '^NEXT_PUBLIC' | \
    sed -r "s/=/ /g" | \
    xargs -n 2 bash -c 'echo "sed -i \"s#PROD_$0#$1#g\""'
}

IFS=$'\n'
for c in $(makeSedCommands); do
  for f in $(find /app -type f -name "*.js" -path "*/.next/*"); do
    eval $c "$f"
  done
done

echo "Starting Circle web"
exec dumb-init node --max-old-space-size=8192 /app/web/server.js

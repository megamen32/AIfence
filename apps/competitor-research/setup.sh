#!/bin/sh
set -eu

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y --no-install-recommends ca-certificates nodejs npm chromium

PLAYWRIGHT_ROOT=/var/agent/playwright
mkdir -p "$PLAYWRIGHT_ROOT"

# playwright-go v0.6401.0 is coupled to Playwright core 1.64.1.
# Use the distro Node.js and Chromium binary; do not download a second browser.
if [ ! -f "$PLAYWRIGHT_ROOT/node_modules/playwright-core/cli.js" ]; then
  npm install --prefix "$PLAYWRIGHT_ROOT" --no-save --no-package-lock --omit=dev playwright-core@1.64.1
fi

test -x /usr/bin/node
test -x /usr/bin/chromium
test -f "$PLAYWRIGHT_ROOT/node_modules/playwright-core/cli.js"

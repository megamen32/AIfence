#!/bin/sh
set -eu
apt-get update
apt-get install -y --no-install-recommends python3 python3-venv chromium
python3 -m venv /opt/aifence-playwright
/opt/aifence-playwright/bin/pip install --no-cache-dir 'playwright==1.55.0'
/opt/aifence-playwright/bin/python -c 'import playwright.sync_api'
chromium --version

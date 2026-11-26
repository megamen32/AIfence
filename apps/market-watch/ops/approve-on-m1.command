#!/bin/bash
set -eu
app="$(cd "$(dirname "$0")/.." && pwd)"
air="$app/../../.runtime/bin/market-air"
origin=http://localhost:42080
# Reuse an existing device flow when possible. The code appears only in this Terminal.
if "$air" login "$origin" --check --json 2>/dev/null | /usr/bin/python3 -c '
import json, sys, subprocess
d=json.load(sys.stdin).get("data",{})
url=d.get("verificationUrl","")
code=d.get("userCode","")
if url!="http://localhost:42080/device-login" or not code: sys.exit(1)
subprocess.run(["/usr/bin/open",url],check=True)
print("Airlock device approval on this Mac:",url)
print("Enter this code there:",code)
'; then
    read -r -p "Press Return after approving Market Watch installation. " unused || true
    exit 0
fi
exec "$air" login "$origin" --wait

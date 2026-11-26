#!/usr/bin/env bash
# The upstream hook constructs a zone-relative TXT owner for the DNS API.
# Append the zone for its authoritative DNS lookups without changing the hook.
set -euo pipefail
zone=${SWEB_ZONE:?Set the DNS zone}
args=()
for arg in "$@"; do
  if [[ "$arg" == _acme-challenge.* && "$arg" != *".$zone" && "$arg" != *".$zone." ]]; then
    arg="$arg.$zone."
  fi
  args+=("$arg")
done
exec /usr/bin/dig "${args[@]}"

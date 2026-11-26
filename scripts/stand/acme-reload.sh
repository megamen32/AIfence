#!/bin/sh
# Certbot deploy hook: reload only the configured stand certificate consumer.
set -eu
config=/srv/aifence-stand/acme/lineage
[ -r "$config" ] || exit 1
IFS= read -r lineage < "$config"
[ -n "$lineage" ] || exit 1
[ "${RENEWED_LINEAGE:-}" = "$lineage" ] || exit 0
/usr/sbin/nginx -t
/bin/systemctl reload nginx

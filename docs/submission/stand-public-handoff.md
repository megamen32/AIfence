# Public stand repair — 2026-11-26

The isolated AIfence Airlock on server-100 was switched from loopback-only
local mode to HTTPS proxy mode. Public origin:
https://aifence.bezrabotnyi.com/

## Applied state

- A Let's Encrypt certificate covering the apex and *.aifence.bezrabotnyi.com
  was issued by the existing stand ACME process. The certificate's validity
  ends 2027-02-24.
- Isolated compose project aifence-stand only: Caddy proxy on 127.0.0.1:42082,
  DOMAIN=aifence.bezrabotnyi.com, TLS_MODE=proxy, HTTPS PUBLIC_URL and S3_URL_PUBLIC,
  exact trusted Docker gateway 172.18.0.1/32 and REVERSE_PROXY_LIMIT=2.
- Original environment retained with mode 0600 at
  /srv/aifence-stand/data/app/.env.aifence-local-before-public.
  The original loopback-only Caddy was left in place for recovery.
- Only the new vhost was added to Nginx sites-available/sites-enabled.
  No existing vhost, SNI map, production Airlock or shared model configuration
  was edited. The proxy replaces client-provided forwarding headers.
- The first vhost attempt was rolled back when the immediate TLS test failed.
  The second passed after the Nginx reload converged. nginx -t passed.

## Verification

Normal HTTPS, without skipping TLS verification, from server-100 and server-44:
apex 200, Product Manager app subdomain 401 (auth required), S3 subdomain 403
(anonymous access forbidden), /health returned status ok, db=true, s3=true.

The Mac's system resolver did not resolve the apex in the last test; public
DNS (1.1.1.1) did, and the Mac's explicit address HTTPS health test passed.
This is a remaining local DNS issue, not an HTTPS certificate failure.

## Follow-up

This was an explicitly requested, scoped operational repair on the server.
The new ingress must be reconciled with the canonical infrastructure
configuration by its owner; unrelated WIP was not edited. Confirm renewal
and Nginx certificate reload in the normal infrastructure workflow.
Do not re-run the local-only stand setup over this public configuration.

No full authenticated Product Manager end-to-end run was performed after
the URL switch. The 64K Gemma profile remains a separate acceptance item.
No real customer data or credentials are included here.

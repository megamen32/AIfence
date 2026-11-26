#!/usr/bin/env bash
set -euo pipefail
test "$(id -un)" = aifence-stand
test "$(hostname)" = "${AIFENCE_EXPECT_HOST:?Set the intended deployment hostname}"
export HOME="$(getent passwd aifence-stand | cut -d: -f6)"
export XDG_RUNTIME_DIR=/run/aifence-stand
export DOCKER_HOST=unix:///run/aifence-stand/docker.sock
export COMPOSE_PARALLEL_LIMIT=1
cd /srv/aifence-stand/data/app
test -S /run/aifence-stand/docker.sock
[[ ! -e .env ]] || { echo 'Existing stand configuration found; bootstrap will not modify it.' >&2; exit 1; }
: "${AIFENCE_PUBLIC_DOMAIN:?Set the public stand hostname}"
: "${AIFENCE_TRUSTED_PROXY:?Set the verified Docker gateway CIDR}"
: "${AIFENCE_PAUSE_BACKGROUND:?Choose true or false after reviewing startup workloads}"
[[ "$AIFENCE_PUBLIC_DOMAIN" =~ ^[a-z0-9.-]+$ && "$AIFENCE_TRUSTED_PROXY" =~ ^[0-9a-fA-F.:/]+$ ]]
[[ "$AIFENCE_PAUSE_BACKGROUND" == true || "$AIFENCE_PAUSE_BACKGROUND" == false ]]

tag=be0f9ee03be3
for pair in \
  airlock:5ae7a58b455f20fbd6067edf410a82e7095d095bbf5466274ae767f6afb05d88 \
  frontend:4905a8707c50d91672575051c21112f6b92a81f068f973e0a59d8c16429dbad5 \
  agent-base:ebd7dc96d20e68e0b9e74aea6cdf4bf9088d21d416466d0789bd5e5af60fb1ca \
  agent-builder:c27823caf48675926953d2f227e2f4dd6612156078a37ab2e3b029fdd3ef66f0 \
  js-executor:13e01b64247c25eb6375e3cf6afa9dd5eca8e978997d9a900ef7862e3b740e9d; do
  name=${pair%%:*}; id=${pair#*:}
  test "$(docker image inspect --format '{{.Id}}' "sha256:$id")" = "sha256:$id"
  test "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "sha256:$id")" = be0f9ee03be31ee15dacd5f6ebcc0f28acea6468
  docker tag "sha256:$id" "aifence-$name:$tag"
done
for image in caddy:2-alpine rustfs/rustfs:latest pgvector/pgvector:pg17; do
  docker image inspect "$image" >/dev/null
done

if [[ ! -e .env ]]; then
  umask 077
  # Only the upstream config renderer is used; it generates fresh secrets here.
  source ./install.sh
  INSTANCE_ID=aifence-stand TLS_MODE=proxy DOMAIN="$AIFENCE_PUBLIC_DOMAIN" INFRA_DB=bundled INFRA_S3=bundled BUILDKIT_HOST_VAL='' BUILD_CADDY=0
  DOCKER_SOCKET_PATH=/run/aifence-stand/docker.sock
  assemble_profiles
  render_env
  cat >> .env <<EOF
PUBLIC_URL=https://$AIFENCE_PUBLIC_DOMAIN
S3_URL_PUBLIC=https://s3.$AIFENCE_PUBLIC_DOMAIN
PROXY_HTTP_PORT=${AIFENCE_PROXY_PORT:-42082}
CADDY_TRUSTED_PROXIES=$AIFENCE_TRUSTED_PROXY
REVERSE_PROXY_LIMIT=2
AIRLOCK_IMAGE=aifence-airlock:$tag
FRONTEND_IMAGE=aifence-frontend:$tag
AGENT_BASE_IMAGE=aifence-agent-base:$tag
AGENT_BUILDER_IMAGE=aifence-agent-builder:$tag
JS_EXECUTOR_IMAGE=aifence-js-executor:$tag
AGENT_MEMORY_LIMIT=1g
EOF
fi
grep -qx 'COMPOSE_PROJECT_NAME=aifence-stand' .env
grep -qx 'DOCKER_SOCKET_PATH=/run/aifence-stand/docker.sock' .env
mkdir -p frontend/dist certs
cat > compose.stand.yml <<EOF
services:
  airlock:
    environment:
      GOMAXPROCS: "2"
      GOFLAGS: "-p=1"
      AIRLOCK_BACKGROUND_ACTIONS_PAUSED: "$AIFENCE_PAUSE_BACKGROUND"
EOF
docker compose -f docker-compose.yml -f compose.stand.yml up -d --no-build --pull never
docker compose -f docker-compose.yml -f compose.stand.yml ps

#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
# State-only relocation. No docker save/load is used.
CTX=colima-aifence
[ -n "${SOURCE_CONTEXT-}" ] && CTX="$SOURCE_CONTEXT" || true
REMOTE=server100
[ -n "${DEST_HOST-}" ] && REMOTE="$DEST_HOST" || true
ROOT=/home/roomhacker/aifence-move
[ -n "${DEST_ROOT-}" ] && ROOT="$DEST_ROOT" || true
HOST=move.aifence.bezrabotnyi.com
[ -n "${PUBLIC_HOST-}" ] && HOST="$PUBLIC_HOST" || true
PORT=44381
[ -n "${CADDY_PORT-}" ] && PORT="$CADDY_PORT" || true
BASEROW_NETWORK=aifence-agent-net-37aa7358-942e-44c4-acd0-b971e83e0ce9
REDMINE_NETWORK=aifence-agent-net-358285c9-3347-436e-812d-55c6d3a61e73
USERIO_NETWORK=aifence-agent-net-870d47ee-6234-4109-a6b3-3e06f960a094
SRC=/Users/user/AIfence
[ -n "${SOURCE_ROOT-}" ] && SRC="$SOURCE_ROOT" || true
D=/opt/homebrew/bin/docker
AIFENCE_SHA=d47e5103b383783f9590b9fbc2887be0a87e8db8
AIRLOCK_SHA=be0f9ee03be31ee15dacd5f6ebcc0f28acea6468
CORE_SHA=0c5951ebb5d32f666d94c7ef909dd8ebb13097ad
AIRLOCK_TAG=personal-0c5951ebb5d3
AGENT_TAG=be0f9ee03be3
log(){ printf '[move-to-server100] %s\n' "$*" >&2; }
die(){ echo "move-to-server100: $*" >&2; exit 1; }
remote(){ ssh -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=120 "$REMOTE" "$@"; }
sync_file(){
  local src="$1" dst="$2" attempt=1
  while ! rsync -a --partial --timeout=900 -e 'ssh -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=120' "$src" "$REMOTE:$dst"; do
    [ "$attempt" -ge 5 ] && return 1
    attempt=$((attempt+1)); sleep 5
  done
}
retry(){
  local cmd="$1" attempt=1
  while ! remote "$cmd"; do
    [ "$attempt" -ge 5 ] && return 1
    attempt=$((attempt+1)); sleep 5
  done
}
mark(){ remote "mkdir -p '$ROOT/.phases' && touch '$ROOT/.phases/$1'"; }
done_phase(){ remote "test -f '$ROOT/.phases/$1'"; }
command -v "$D" >/dev/null || die "Docker CLI missing"
"$D" --context "$CTX" info >/dev/null || die "source context unavailable"
remote true || die "destination unavailable"
remote 'docker info >/dev/null' || die "destination Docker unavailable"
remote "test ! -e '$ROOT' || test -f '$ROOT/.move-managed'" || die "destination is not managed"
remote "mkdir -p '$ROOT'/airlock '$ROOT'/build '$ROOT'/dumps '$ROOT'/volumes '$ROOT'/secrets '$ROOT'/apps '$ROOT'/pin; touch '$ROOT/.move-managed'"
TMP=$(mktemp -d /tmp/aifence-move.XXXX)
trap 'rm -rf "$TMP"' EXIT
TMP_PIN="$TMP/containers.tsv"; TMP_APPS="$TMP/apps.tsv"; TMP_GRANTS="$TMP/grants.tsv"
TMP_METRIKA="$TMP/metrika.env"; TMP_AIRLOCK="$TMP/airlock.env"

if ! done_phase pin; then
  log "pin: capture exact 42080 release and app source refs"
  source_revision=$("$D" --context "$CTX" inspect aifence-airlock-1 --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
  [ "$source_revision" = "$CORE_SHA" ] || die "Airlock revision changed: $source_revision"
  "$D" --context "$CTX" ps --format '{{.Names}}|{{.Image}}|{{.ID}}' | sort > "$TMP_PIN"
  "$D" --context "$CTX" exec aifence-postgres-1 psql -At -F '|' -U airlock -d airlock -c "select id,slug,source_ref,image_ref from agents where status in ('active','building') order by slug" > "$TMP_APPS"
  "$D" --context "$CTX" exec aifence-postgres-1 psql -At -F '|' -U airlock -d airlock -c "select agent_id,grantee_id,role from agent_grants order by agent_id,grantee_id,role" > "$TMP_GRANTS"
  sync_file "$TMP_PIN" "$ROOT/pin/containers.tsv"
  sync_file "$TMP_APPS" "$ROOT/pin/apps.tsv"
  sync_file "$TMP_GRANTS" "$ROOT/pin/grants.tsv"
  remote "printf '%s\n' 'aifence_sha=$AIFENCE_SHA' 'airlock_source_sha=$AIRLOCK_SHA' 'airlock_release_sha=$CORE_SHA' 'airlock_tag=$AIRLOCK_TAG' 'agent_tag=$AGENT_TAG' > '$ROOT/pin/release.env'"
  mark pin
fi

if ! done_phase build; then
  log "build: clone pinned sources and build native amd64 platform"
  retry "if test -d '$ROOT/build/AIfence/.git'; then git -C '$ROOT/build/AIfence' fetch --depth=1 origin '$AIFENCE_SHA'; else git clone --filter=blob:none https://github.com/megamen32/AIfence.git '$ROOT/build/AIfence'; fi; git -C '$ROOT/build/AIfence' checkout --detach '$AIFENCE_SHA'"
  retry "if test -d '$ROOT/build/airlock/.git'; then git -C '$ROOT/build/airlock' fetch --depth=1 origin '$AIRLOCK_SHA'; else git clone --filter=blob:none https://github.com/airlockrun/airlock-internal.git '$ROOT/build/airlock'; fi; git -C '$ROOT/build/airlock' checkout --detach '$AIRLOCK_SHA'"
  retry "cd '$ROOT/build/airlock' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$AIRLOCK_SHA -t aifence-agent-base:$AGENT_TAG -f Dockerfile.agent-base ."
  retry "cd '$ROOT/build/airlock' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$AIRLOCK_SHA -t aifence-agent-builder:$AGENT_TAG -f Dockerfile.agent-builder ."
  retry "cd '$ROOT/build/airlock' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$AIRLOCK_SHA -t aifence-js-executor:$AGENT_TAG -f Dockerfile.js-executor ."
  retry "cd '$ROOT/build/airlock' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$AIRLOCK_SHA -t aifence-airlock:$AGENT_TAG -f Dockerfile.airlock ."
  retry "cd '$ROOT/build/airlock' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$AIRLOCK_SHA -t aifence-frontend:$AGENT_TAG -f Dockerfile.frontend ."
  sync_file "$SRC/.runtime/personal-releases/2026.11.27.6/backend.tar.gz" "$ROOT/build/backend.tar.gz"
  sync_file "$SRC/.runtime/personal-releases/2026.11.27.6/frontend.tar.gz" "$ROOT/build/frontend.tar.gz"
  remote "printf '%s\\n' 'FROM aifence-airlock:$AGENT_TAG' 'ADD backend.tar.gz /' > '$ROOT/build/Dockerfile.airlock-personal'; printf '%s\\n' 'FROM aifence-frontend:$AGENT_TAG' 'ADD frontend.tar.gz /' > '$ROOT/build/Dockerfile.frontend-personal"
  retry "cd '$ROOT/build' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$CORE_SHA -t aifence-airlock:$AIRLOCK_TAG -f Dockerfile.airlock-personal ."
  retry "cd '$ROOT/build' && docker build --platform linux/amd64 --label org.opencontainers.image.revision=$CORE_SHA -t aifence-frontend:$AIRLOCK_TAG -f Dockerfile.frontend-personal ."
  retry "docker pull --platform linux/amd64 pgvector/pgvector:pg17 postgres:16-alpine rustfs/rustfs:latest redmine:6.1.6-alpine3.23 baserow/baserow:2.4.3 caddy:2-alpine curlimages/curl:latest"
  mark build
fi

if ! done_phase state; then
  log "state: rsync compose/config, dump all DBs, archive non-DB volumes"
  sync_file "$SRC/.runtime/airlock/caddy/" "$ROOT/airlock/caddy/"
  sync_file "$SRC/.runtime/airlock/db/" "$ROOT/airlock/db/"
  sync_file "$SRC/apps/baserow/compose.yml" "$ROOT/apps-baserow.compose.yml"
  sync_file "/Users/user/Airlock-apps/redmine-runtime/compose.yaml" "$ROOT/redmine.compose.yml"
  sync_file "/Users/user/Airlock-apps/redmine-runtime/plugins/" "$ROOT/redmine-plugins/"
  sync_file "/Users/user/Airlock-apps/redmine-runtime/sso-public.pem" "$ROOT/redmine-sso-public.pem"
  sync_file "/Users/user/AIfence-worktrees/redmine-dion/scripts/stand/redmine-mcp/" "$ROOT/apps/redmine-mcp/"
  sync_file "$SRC/.runtime/m1-native-sourcewatch-20261127/gateway/" "$ROOT/apps/metrika/"
  sync_file "/Users/user/apps/userio-airlock-standalone/" "$ROOT/apps/userio/"
  sync_file "/Users/user/Library/Application Support/airlock-redmine/db-password" "$ROOT/secrets/redmine-db-password"
  sync_file "/Users/user/Library/Application Support/airlock-redmine/rails-secret" "$ROOT/secrets/redmine-rails-secret"
  sync_file "/Users/user/apps/userio-airlock-standalone/private/" "$ROOT/secrets/userio-private/"
  python3 -c 'import json,subprocess,sys; c=sys.argv[1]; out=sys.argv[2]; x=json.loads(subprocess.check_output(["/opt/homebrew/bin/docker","--context",c,"inspect","aifence-metrika-metrika-mcp-1"],text=True))[0]["Config"]["Env"]; open(out,"w").write("\n".join(v for v in x if v.split("=",1)[0] in {"YANDEX_METRIKA_TOKEN","SUPERGATEWAY_API_KEY"})+"\n")' "$CTX" "$TMP_METRIKA"
  grep -E '^(ENCRYPTION_KEY|JWT_SECRET|REVERSE_PROXY_AUTH_SECRET|POSTGRES_PASSWORD|AIRLOCK_DB_PASSWORD|S3_ACCESS_KEY|S3_SECRET_KEY)=' "$SRC/.runtime/airlock/.env" > "$TMP_AIRLOCK"
  chmod 600 "$TMP_METRIKA" "$TMP_AIRLOCK"
  sync_file "$TMP_METRIKA" "$ROOT/secrets/metrika.env"
  sync_file "$TMP_AIRLOCK" "$ROOT/secrets/airlock.env"
  remote "chmod -R go-rwx '$ROOT/secrets'; find '$ROOT/secrets' -type d -exec chmod u+rwx,go-rwx {} +; find '$ROOT/secrets' -type f -exec chmod u+rw,go-rwx {} +"
  dump(){
    local c="$1" u="$2" mode="$3" p="$4" q db safe
    if [ "$mode" = peer ]; then q=$("$D" --context "$CTX" exec -u postgres "$c" psql -At -U postgres -d postgres -c 'select datname from pg_database where datistemplate=false')
    elif [ "$mode" = secret ]; then q=$("$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$(cat /run/secrets/db-password) psql -At -U '$u' -d postgres -c \"select datname from pg_database where datistemplate=false\"")
    else q=$("$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$POSTGRES_PASSWORD psql -At -U '$u' -d postgres -c \"select datname from pg_database where datistemplate=false\"")
    fi
    while IFS= read -r db; do
      [ -z "$db" ] && continue
      safe=$(printf '%s' "$db" | tr -c 'A-Za-z0-9_.-' '_')
      log "pg_dump $p/$db"
      if [ "$mode" = peer ]; then "$D" --context "$CTX" exec -u postgres "$c" pg_dump -Fc -d "$db" > "$TMP/$p--$safe.dump"
      elif [ "$mode" = secret ]; then "$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$(cat /run/secrets/db-password) pg_dump -Fc -U '$u' -d '$db'" > "$TMP/$p--$safe.dump"
      else "$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$POSTGRES_PASSWORD pg_dump -Fc -U '$u' -d '$db'" > "$TMP/$p--$safe.dump"
      fi
      sync_file "$TMP/$p--$safe.dump" "$ROOT/dumps/$p--$safe.dump"
    done <<< "$q"
  }
  dump aifence-postgres-1 airlock env airlock
  dump aifence-personal-redmine-database-1 redmine secret redmine
  dump aifence-baserow-baserow-1 postgres peer baserow
  volume_archive(){
    local n="$1"
    local out="$TMP/vol-$n.tar.gz"
    if [ "$n" = aifence-baserow_data ]; then
      "$D" --context "$CTX" run --rm -v "$n:/from:ro" caddy:2-alpine sh -c 'tar -C /from --exclude=./postgres -czf - .' > "$out"
    else
      "$D" --context "$CTX" run --rm -v "$n:/from:ro" caddy:2-alpine sh -c 'tar -C /from -czf - .' > "$out"
    fi
    sync_file "$out" "$ROOT/volumes/$n.tar.gz"
  }
  for v in aifence-data aifence_rustfsdata aifence_caddy-data aifence_caddy-config aifence-baserow_data aifence-personal-redmine_files userio-airlock-standalone_state; do
    log "archive volume $v"; volume_archive "$v"
  done
  mark state
fi

if ! done_phase restore; then
  log "restore: start only DB services and restore before app containers"
  sync_file "$SRC/.runtime/airlock/docker-compose.yml" "$ROOT/airlock/docker-compose.yml"
  cat > "$TMP/airlock.override.yml" <<EOF
services:
  airlock: {platform: linux/amd64}
  frontend: {platform: linux/amd64}
  postgres: {platform: linux/amd64}
  postgres-bootstrap: {platform: linux/amd64}
  caddy-proxy: {platform: linux/amd64}
EOF
  sync_file "$TMP/airlock.override.yml" "$ROOT/airlock/compose.server100.yml"
  cat > "$TMP/redmine-db.override.yml" <<EOF
services:
  database: {platform: linux/amd64}
secrets:
  db-password: {file: '$ROOT/secrets/redmine-db-password'}
  rails-secret: {file: '$ROOT/secrets/redmine-rails-secret'}
EOF
  sync_file "$TMP/redmine-db.override.yml" "$ROOT/redmine-db.server100.yml"
  remote "cp '$ROOT/build/airlock/.env.example' '$ROOT/airlock/.env'; cat '$ROOT/secrets/airlock.env' >> '$ROOT/airlock/.env'; printf '%s\n' 'COMPOSE_PROJECT_NAME=aifence-move' 'COMPOSE_PROFILES=bundled-db,bundled-s3,caddy-proxy' 'TLS_MODE=proxy' 'CADDY_TRUSTED_PROXIES=192.168.128.0/20' 'DOMAIN=$HOST' 'PUBLIC_URL=https://$HOST' 'S3_URL_PUBLIC=https://s3.$HOST' 'PROXY_HTTP_PORT=$PORT' 'HTTP_PORT=$PORT' 'DOCKER_DEFAULT_PLATFORM=linux/amd64' 'CADDY_IMAGE=caddy:2-alpine' 'AIRLOCK_IMAGE=aifence-airlock:$AIRLOCK_TAG' 'FRONTEND_IMAGE=aifence-frontend:$AIRLOCK_TAG' 'AGENT_BUILDER_IMAGE=aifence-agent-builder:$AGENT_TAG' 'AGENT_BASE_IMAGE=aifence-agent-base:$AGENT_TAG' 'JS_EXECUTOR_IMAGE=aifence-js-executor:$AGENT_TAG' >> '$ROOT/airlock/.env'; chmod 600 '$ROOT/airlock/.env'"
  remote "docker compose -p aifence-move --env-file '$ROOT/airlock/.env' -f '$ROOT/airlock/docker-compose.yml' -f '$ROOT/airlock/compose.server100.yml' up -d postgres"
  for v in aifence-data aifence_rustfsdata aifence_caddy-data aifence_caddy-config aifence-baserow_data aifence-personal-redmine_files userio-airlock-standalone_state; do remote "docker volume create $v >/dev/null || true; docker run --rm -v '$v:/to' -v '$ROOT/volumes/$v.tar.gz:/archive:ro' caddy:2-alpine sh -c 'tar -C /to -xzf /archive' >/tmp/aifence-volume-$v.log 2>&1 & pid=\$!; while kill -0 \$pid 2>/dev/null; do printf .; sleep 10; done; wait \$pid; rc=\$?; printf '\n'; if [ \$rc -ne 0 ]; then tail -20 /tmp/aifence-volume-$v.log >&2; exit \$rc; fi"; done
  remote "docker compose -p aifence-move-redmine -f '$ROOT/redmine.compose.yml' -f '$ROOT/redmine-db.server100.yml' up -d database"
  remote "docker network inspect '$BASEROW_NETWORK' >/dev/null 2>&1 || docker network create '$BASEROW_NETWORK' >/dev/null; docker network inspect '$REDMINE_NETWORK' >/dev/null 2>&1 || docker network create '$REDMINE_NETWORK' >/dev/null; docker network inspect '$USERIO_NETWORK' >/dev/null 2>&1 || docker network create '$USERIO_NETWORK' >/dev/null; BASEROW_PUBLIC_URL='https://$HOST' BASEROW_AIRLOCK_NETWORK='$BASEROW_NETWORK' BASEROW_PLATFORM=linux/amd64 docker compose -p aifence-move-baserow -f '$ROOT/apps-baserow.compose.yml' up -d baserow"
  remote "pgid=\$(docker compose -p aifence-move --env-file '$ROOT/airlock/.env' -f '$ROOT/airlock/docker-compose.yml' ps -q postgres); for db in postgres airlock; do docker cp '$ROOT/dumps/airlock--'\$db'.dump' \"\$pgid:/tmp/restore.dump\"; docker exec \"\$pgid\" chmod 644 /tmp/restore.dump; docker exec \"\$pgid\" sh -c 'PGPASSWORD=\$POSTGRES_PASSWORD pg_restore --clean --if-exists --no-owner --no-privileges -U airlock -d \"\$1\" /tmp/restore.dump' sh \"\$db\"; done"
  remote "docker exec \"\$(docker compose -p aifence-move --env-file '$ROOT/airlock/.env' -f '$ROOT/airlock/docker-compose.yml' ps -q postgres)\" psql -v ON_ERROR_STOP=1 -U airlock -d airlock -c 'ALTER SCHEMA public OWNER TO airlock_app; GRANT USAGE,CREATE ON SCHEMA public TO airlock_app; GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO airlock_app; GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO airlock_app; GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO airlock_app'"
  remote "rid=\$(docker compose -p aifence-move-redmine -f '$ROOT/redmine.compose.yml' ps -q database); for db in postgres redmine; do docker cp '$ROOT/dumps/redmine--'\$db'.dump' \"\$rid:/tmp/restore.dump\"; docker exec \"\$rid\" chmod 644 /tmp/restore.dump; docker exec \"\$rid\" sh -c 'PGPASSWORD=\$(cat /run/secrets/db-password) pg_restore --clean --if-exists --no-owner --no-privileges -U redmine -d \"\$1\" /tmp/restore.dump' sh \"\$db\"; done"
  remote "bid=\$(BASEROW_PUBLIC_URL='https://$HOST' BASEROW_AIRLOCK_NETWORK='$BASEROW_NETWORK' BASEROW_PLATFORM=linux/amd64 docker compose -p aifence-move-baserow -f '$ROOT/apps-baserow.compose.yml' ps -q baserow); for db in postgres baserow; do docker cp '$ROOT/dumps/baserow--'\$db'.dump' \"\$bid:/tmp/restore.dump\"; docker exec \"\$bid\" chmod 644 /tmp/restore.dump; docker exec -u postgres \"\$bid\" sh -c 'pg_restore --clean --if-exists --no-owner --no-privileges -U postgres -d \"\$1\" /tmp/restore.dump' sh \"\$db\"; done"
  mark restore
fi

if ! done_phase up; then
  log "up: build native sidecars and start services"
  cat > "$TMP/redmine.override.yml" <<EOF
services:
  database: {platform: linux/amd64}
  redmine:
    platform: linux/amd64
    volumes:
      - files:/usr/src/redmine/files
      - '$ROOT/redmine-plugins/airlock_sso:/usr/src/redmine/plugins/airlock_sso'
      - '$ROOT/redmine-sso-public.pem:/run/airlock-sso/public.pem:ro'
    secrets: [db-password, rails-secret]
secrets:
  db-password: {file: '$ROOT/secrets/redmine-db-password'}
  rails-secret: {file: '$ROOT/secrets/redmine-rails-secret'}
EOF
  sync_file "$TMP/redmine.override.yml" "$ROOT/redmine.server100.yml"
  cat > "$TMP/baserow.override.yml" <<EOF
services:
  baserow: {platform: linux/amd64}
EOF
  sync_file "$TMP/baserow.override.yml" "$ROOT/baserow.server100.yml"
  cat > "$TMP/redmine-mcp.override.yml" <<EOF
services:
  redmine-mcp: {platform: linux/amd64}
networks:
  aifence: {external: true, name: airlock}
  redmine: {external: true, name: aifence-move-redmine_backend}
  connector: {external: true, name: aifence-redmine-mcp_connector}
EOF
  sync_file "$TMP/redmine-mcp.override.yml" "$ROOT/apps/redmine-mcp/compose.server100.yml"
  cat > "$TMP/metrika.override.yml" <<EOF
services:
  metrika-mcp:
    platform: linux/amd64
    image: aifence-metrika-metrika-mcp:server100
    network_mode: container:aifence-move-airlock-1
EOF
  sync_file "$TMP/metrika.override.yml" "$ROOT/apps/metrika/compose.server100.yml"
  cat > "$TMP/userio.override.yml" <<EOF
services:
  userio:
    platform: linux/amd64
    image: userio-airlock:56c66260-mcp-oauth-amd64
    environment:
      USERIO_AIRLOCK_OAUTH_BASE: https://$HOST
      USERIO_AIRLOCK_OAUTH_AUTHORIZE: https://$HOST/authorize
      USERIO_AIRLOCK_OAUTH_CALLBACK: https://$HOST/api/v1/credentials/oauth/callback
    volumes:
      - bootstrap:/run/userio-bootstrap:ro
  mcp-local:
    platform: linux/amd64
    network_mode: container:aifence-move-airlock-1
volumes:
  bootstrap:
    external: true
    name: userio-airlock-standalone-move_bootstrap
EOF
  sync_file "$TMP/userio.override.yml" "$ROOT/apps/userio/compose.server100.yml"
  remote "docker volume create userio-airlock-standalone-move_bootstrap >/dev/null; docker run --rm -v userio-airlock-standalone-move_bootstrap:/to -v '$ROOT/apps/userio/private:/from:ro' caddy:2-alpine sh -c 'rm -f /to/*; cp /from/* /to/; chmod 755 /to; chmod 600 /to/owner.env /to/service-token; chmod 644 /to/cabinet-public-key'"
  cat > "$TMP/userio.Dockerfile.server100" <<'EOF'
FROM golang:1.26.1-trixie AS dion-helper
WORKDIR /build
COPY tools/dion-call-helper .
RUN CGO_ENABLED=0 GOMAXPROCS=2 go build -p=1 -trimpath -o /out/userio-dion-call-helper .
FROM python:3.12-slim-trixie
ARG USERIO_REVISION
LABEL org.opencontainers.image.revision=${USERIO_REVISION}
WORKDIR /opt/universal-userio
COPY pyproject.toml .
COPY src ./src
RUN pip install --no-cache-dir '.[fast-agent,max,telegram,chatgpt]' matrix-nio==0.26.0 && python -c "from universal_userio.runtime import main; from universal_userio.messenger_dion import DionMessengerProvider; from universal_userio.max_accounts import MaxAccounts"
COPY --from=dion-helper /out/userio-dion-call-helper /opt/userio-dion-call-helper
COPY entrypoint.py test_airlock_identity.py ./ 
ENV PYTHONUNBUFFERED=1 USERIO_HOST=0.0.0.0 USERIO_PORT=18093 USERIO_DB_PATH=/var/lib/universal-userio/userio.sqlite3 USERIO_CONNECTION_SECRETS_DIR=/var/lib/universal-userio/connected-secrets USERIO_AI_ENDPOINT=http://127.0.0.1:9/v1 USERIO_AI_TOKEN=disabled USERIO_AI_MODEL=disabled USERIO_OWNER_SEED_FILE=/run/no-owner-reseed
EXPOSE 18093
CMD ["python","entrypoint.py"]
EOF
  sync_file "$TMP/userio.Dockerfile.server100" "$ROOT/apps/userio/source/Dockerfile.server100"
  cat > "$TMP/userio.Dockerfile.demo-amd64" <<'EOF'
FROM userio-airlock:56c66260-pre-cabinet-amd64
LABEL io.userio.local.overlay="airlock-cabinet-v1"
COPY src/universal_userio/airlock_identity.py src/universal_userio/http_api.py /opt/universal-userio/src/universal_userio/
COPY test_airlock_identity.py test_cabinet_http.py /opt/universal-userio/
COPY src/universal_userio/static/connect-accounts.html /opt/universal-userio/src/universal_userio/static/connect-accounts.html
EOF
  sync_file "$TMP/userio.Dockerfile.demo-amd64" "$ROOT/apps/userio/source/Dockerfile.demo-amd64"
  cat > "$TMP/userio.Dockerfile.demo-ui-amd64" <<'EOF'
FROM userio-airlock:56c66260-demo-amd64
COPY src/universal_userio/static/index.html /opt/universal-userio/src/universal_userio/static/index.html
COPY src/universal_userio/static/assets/ /opt/universal-userio/src/universal_userio/static/assets/
EOF
  sync_file "$TMP/userio.Dockerfile.demo-ui-amd64" "$ROOT/apps/userio/source/Dockerfile.demo-ui-amd64"
  cat > "$TMP/userio.Dockerfile.qr-workers-amd64" <<'EOF'
FROM node:22-slim AS node
FROM golang:1.26.1-trixie AS deps
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/include/node /usr/local/include/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && git config --global url.https://github.com/.insteadOf ssh://git@github.com/ && test "$(uname -m)" = x86_64 && node --version && npm --version
ENV npm_config_nodedir=/usr/local
WORKDIR /opt/userio-telegram-qr
COPY qr-workers/telegram/ ./
RUN --mount=type=cache,target=/root/.npm npm ci --omit=dev --no-audit --no-fund
WORKDIR /opt/userio-whatsapp-qr
COPY qr-workers/whatsapp/ ./
RUN --mount=type=cache,target=/root/.npm npm ci --omit=dev --no-audit --no-fund
FROM userio-airlock:56c66260-demo-ui-amd64
RUN apt-get update && apt-get install -y --no-install-recommends age ca-certificates libatomic1 libstdc++6 && rm -rf /var/lib/apt/lists/*
COPY --from=deps /usr/local/bin/node /usr/local/bin/node
COPY --from=deps /opt/userio-telegram-qr /opt/userio-telegram-qr
COPY --from=deps /opt/userio-whatsapp-qr /opt/userio-whatsapp-qr
RUN node --version && command -v age && command -v age-keygen
EOF
  sync_file "$TMP/userio.Dockerfile.qr-workers-amd64" "$ROOT/apps/userio/source/Dockerfile.qr-workers-amd64"
  cat > "$TMP/userio.Dockerfile.qr-live-amd64" <<'EOF'
FROM userio-airlock:56c66260-qr-workers-amd64
COPY src/universal_userio/qr_gateway.py src/universal_userio/http_api.py /opt/universal-userio/src/universal_userio/
COPY entrypoint.py qr_workers.py configure_telegram.py /opt/universal-userio/
COPY test_qr_gateway.py /opt/universal-userio/
EOF
  sync_file "$TMP/userio.Dockerfile.qr-live-amd64" "$ROOT/apps/userio/source/Dockerfile.qr-live-amd64"
  cat > "$TMP/userio.Dockerfile.mcp-oauth-amd64" <<'EOF'
FROM userio-airlock:56c66260-qr-live-amd64
COPY src/universal_userio/http_api.py /opt/universal-userio/src/universal_userio/http_api.py
COPY test_airlock_oauth.py /opt/universal-userio/
EOF
  sync_file "$TMP/userio.Dockerfile.mcp-oauth-amd64" "$ROOT/apps/userio/source/Dockerfile.mcp-oauth-amd64"
  remote "cd '$ROOT/apps/redmine-mcp' && docker build --platform linux/amd64 -t aifence-redmine-mcp:2.23.0 ."
  remote "cd '$ROOT/apps/metrika' && docker build --platform linux/amd64 -t aifence-metrika-metrika-mcp:server100 -f metrika.Dockerfile ."
  remote "if docker image inspect userio-airlock:56c66260-mcp-oauth-amd64 >/dev/null 2>&1; then echo reuse-native-userio; else cd '$ROOT/apps/userio/source' && docker build --platform linux/amd64 -t userio-airlock:56c66260-arm64 --build-arg USERIO_REVISION=56c66260f2c1bf75c4b615e369fd0e032184e0ee -f Dockerfile.server100 . && docker tag userio-airlock:56c66260-arm64 userio-airlock:56c66260-pre-cabinet-amd64 && docker build --platform linux/amd64 -t userio-airlock:56c66260-demo-amd64 -f Dockerfile.demo-amd64 . && docker build --platform linux/amd64 -t userio-airlock:56c66260-demo-ui-amd64 -f Dockerfile.demo-ui-amd64 . && docker build --platform linux/amd64 -t userio-airlock:56c66260-qr-workers-amd64 -f Dockerfile.qr-workers-amd64 . && docker build --platform linux/amd64 -t userio-airlock:56c66260-qr-live-amd64 -f Dockerfile.qr-live-amd64 . && docker build --platform linux/amd64 -t userio-airlock:56c66260-mcp-oauth-amd64 -f Dockerfile.mcp-oauth-amd64 .; fi"
  remote "if test -d '$ROOT/build/AIfence'; then cd '$ROOT/build/AIfence' && docker build --platform linux/amd64 -t aifence-app-competitor-research:$AIFENCE_SHA -f apps/competitor-research/Dockerfile apps/competitor-research || true; docker build --platform linux/amd64 -t aifence-app-market-watch:$AIFENCE_SHA -f apps/market-watch/Dockerfile apps/market-watch || true; docker build --platform linux/amd64 -t aifence-app-product-manager:$AIFENCE_SHA -f apps/product-manager/Dockerfile apps/product-manager || true; fi"
  remote "for image in rustfs/rustfs:latest alpine/socat:1.8.0.3 moby/buildkit:rootless cloudflare/cloudflared:latest caddy:2-alpine; do docker image inspect \"\$image\" >/dev/null 2>&1 || docker pull --platform linux/amd64 \"\$image\"; done"
  remote "cd '$ROOT/airlock' && docker compose -p aifence-move --env-file .env -f docker-compose.yml -f compose.server100.yml up -d --no-build --pull never"
  remote "cd '$ROOT' && docker compose -p aifence-move-redmine -f redmine.compose.yml -f redmine.server100.yml up -d --no-build --pull never"
  remote "cd '$ROOT' && BASEROW_PUBLIC_URL='https://$HOST' BASEROW_AIRLOCK_NETWORK='$BASEROW_NETWORK' BASEROW_PLATFORM=linux/amd64 docker compose -p aifence-move-baserow -f apps-baserow.compose.yml -f baserow.server100.yml up -d --no-build --pull never"
  remote "docker network inspect aifence-redmine-mcp_connector >/dev/null 2>&1 || docker network create aifence-redmine-mcp_connector >/dev/null; cd '$ROOT/apps/redmine-mcp' && docker compose -p aifence-move-redmine-mcp -f compose.yaml -f compose.server100.yml up -d --no-build --pull never"
  remote "cd '$ROOT/apps/metrika' && AIFENCE_CORE_CONTAINER=aifence-move-airlock-1 docker compose -p aifence-move-metrika --env-file '$ROOT/secrets/metrika.env' -f metrika.compose.yml -f compose.server100.yml up -d --no-build --pull never"
  remote "cd '$ROOT/apps/userio' && docker compose -p userio-airlock-standalone-move -f compose.yaml -f compose.server100.yml up -d --no-build --pull never"
  mark up
fi

if ! done_phase reconcile; then
  log "reconcile: verify restored app generations and grants"
  remote "pgid=\$(docker compose -p aifence-move --env-file '$ROOT/airlock/.env' -f '$ROOT/airlock/docker-compose.yml' ps -q postgres); docker exec \$pgid psql -At -F '|' -U airlock -d airlock -c \"select id,slug,source_ref,image_ref from agents where status in ('active','building') order by slug\" | sha256sum | cut -d' ' -f1 > '$ROOT/pin/actual-apps.sha256'; expected=\$(sha256sum '$ROOT/pin/apps.tsv' | cut -d' ' -f1); test \"\$(cat '$ROOT/pin/actual-apps.sha256')\" = \"\$expected\" || { echo 'Airlock app generation/source hash mismatch' >&2; exit 1; }; docker exec \$pgid psql -At -F '|' -U airlock -d airlock -c 'select agent_id,grantee_id,role from agent_grants order by agent_id,grantee_id,role' | sha256sum | cut -d' ' -f1 > '$ROOT/pin/actual-grants.sha256'; expected=\$(sha256sum '$ROOT/pin/grants.tsv' | cut -d' ' -f1); test \"\$(cat '$ROOT/pin/actual-grants.sha256')\" = \"\$expected\" || { echo 'Airlock grant hash mismatch' >&2; exit 1; }; docker exec \$pgid psql -At -U airlock -d airlock -c \"select count(*) from agents where status in ('active','building')\" > '$ROOT/pin/active-count'; docker exec \$pgid psql -At -U airlock -d airlock -c 'select count(*) from agent_grants' > '$ROOT/pin/agent-grants-count'; docker exec \$pgid psql -At -U airlock -d airlock -c 'select count(*) from resource_grants' > '$ROOT/pin/resource-grants-count'"
  mark reconcile
fi

if ! done_phase health; then
  log "health: configure HTTPS edge and check Airlock"
  cat > "$TMP/nginx.conf" <<EOF
server {
    listen 127.0.0.1:8444 ssl;
    server_name $HOST;
    ssl_certificate /etc/letsencrypt/live/aifence.bezrabotnyi.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/aifence.bezrabotnyi.com/privkey.pem;
    client_max_body_size 64m;
    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host \$host;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_buffering off;
    }
}
EOF
  cat "$TMP/nginx.conf" | remote "sudo tee /etc/nginx/sites-available/aifence-move.bezrabotnyi.com >/dev/null && sudo ln -sfn /etc/nginx/sites-available/aifence-move.bezrabotnyi.com /etc/nginx/sites-enabled/aifence-move.bezrabotnyi.com && sudo nginx -t && sudo systemctl reload nginx"
  remote "curl -fsS --max-time 20 -H 'Host: $HOST' http://127.0.0.1:$PORT/health >/dev/null"
  remote "curl -fsS --max-time 20 -H 'Host: $HOST' http://127.0.0.1:$PORT/ >/dev/null"
  remote "printf '%s\n' 'source_aifence_sha=$AIFENCE_SHA' 'source_airlock_sha=$AIRLOCK_SHA' 'release_core_sha=$CORE_SHA' 'public_host=$HOST' 'caddy_loopback_port=$PORT' > '$ROOT/MOVE_RECEIPT'"
  mark health
fi
log "complete: https://$HOST"


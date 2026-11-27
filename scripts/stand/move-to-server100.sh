#!/usr/bin/env bash
set -Eeuo pipefail
mapfile(){ local name="$2" line; eval "$name=()"; while IFS= read -r line; do eval "$name+=(\"$line\")"; done; }
umask 077
CTX=${SOURCE_CONTEXT:-colima-aifence}
REMOTE=${REMOTE:-server100}
ROOT=${DEST_ROOT:-/home/roomhacker/aifence-move}
HOST=${PUBLIC_HOST:-move.aifence.bezrabotnyi.com}
PORT=${CADDY_PORT:-44380}
SRC=${SOURCE_ROOT:-/Users/user/AIfence}
D=/opt/homebrew/bin/docker
SSH=(ssh -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=120 "$REMOTE")
R=(rsync -a --delete -e "ssh -o BatchMode=yes" )
log(){ printf '[move-to-server100] %s\n' "$*" >&2; }
remote(){ "${SSH[@]}" "$@"; }
die(){ echo "move-to-server100: $*" >&2; exit 1; }
command -v "$D" >/dev/null || die "Docker CLI missing"
"$D" --context "$CTX" info >/dev/null || die "source context unavailable"
remote true || die "destination unavailable"
remote 'docker info >/dev/null' || die "destination docker unavailable"
[[ "$HOST" =~ ^[a-z0-9][a-z0-9.-]+$ ]] || die "bad host"
[[ "$PORT" =~ ^[0-9]+$ ]] || die "bad port"
remote "test ! -e '$ROOT' || test -f '$ROOT/.move-managed'" || die "destination exists and is not managed"
TMP=$(mktemp -d "${TMPDIR:-/tmp}/aifence-move.XXXX"); trap 'rm -rf "$TMP"' EXIT
remote "mkdir -p '$ROOT'/airlock/{caddy,db} '$ROOT'/caddy-market-iframe '$ROOT'/apps/{baserow,redmine-mcp,metrika,userio} '$ROOT'/redmine '$ROOT'/secrets/userio-private '$ROOT'/dumps '$ROOT'/volumes; touch '$ROOT/.move-managed'"
sync(){ local src="$1" dst="$2" attempt=1; while ! rsync -a --delete --partial --timeout=900 -e 'ssh -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=120' "$src" "$REMOTE:$dst"; do [[ $attempt -ge 4 ]] && return 1; attempt=$((attempt+1)); sleep 2; done; }
log "copying compose, Caddy and app sources"
sync "$SRC/.runtime/airlock/docker-compose.yml" "$ROOT/airlock/docker-compose.yml"
sync "$SRC/.runtime/airlock/caddy/" "$ROOT/airlock/caddy/"
sync "$SRC/.runtime/airlock/db/" "$ROOT/airlock/db/"
sync "$SRC/.runtime/caddy-market-iframe/" "$ROOT/caddy-market-iframe/"
sync "$SRC/apps/baserow/compose.yml" "$ROOT/apps/baserow/compose.yml"
sync "/Users/user/Airlock-apps/redmine-runtime/compose.yaml" "$ROOT/redmine/compose.yaml"
sync "/Users/user/Airlock-apps/redmine-runtime/plugins/" "$ROOT/redmine/plugins/"
sync "/Users/user/Airlock-apps/redmine-runtime/sso-public.pem" "$ROOT/redmine/sso-public.pem"
sync "/Users/user/AIfence-worktrees/redmine-dion/scripts/stand/redmine-mcp/" "$ROOT/apps/redmine-mcp/"
sync "$SRC/.runtime/m1-native-sourcewatch-20261127/gateway/" "$ROOT/apps/metrika/"
sync "/Users/user/apps/userio-airlock-standalone/" "$ROOT/apps/userio/"
sync "/Users/user/Library/Application Support/airlock-redmine/db-password" "$ROOT/secrets/redmine-db-password"
sync "/Users/user/Library/Application Support/airlock-redmine/rails-secret" "$ROOT/secrets/redmine-rails-secret"
sync "/Users/user/apps/userio-airlock-standalone/private/" "$ROOT/secrets/userio-private/"
log "capturing protected runtime env (values never printed)"
python3 -c 'import json,subprocess,sys; c=sys.argv[1]; out=sys.argv[2]; x=json.loads(subprocess.check_output(["/opt/homebrew/bin/docker","--context",c,"inspect","aifence-metrika-metrika-mcp-1"],text=True))[0]["Config"]["Env"]; open(out,"w").write("\n".join(v for v in x if v.split("=",1)[0] in {"YANDEX_METRIKA_TOKEN","SUPERGATEWAY_API_KEY"})+"\n")' "$CTX" "$TMP/metrika.env"
grep -E '^(ENCRYPTION_KEY|JWT_SECRET|REVERSE_PROXY_AUTH_SECRET|POSTGRES_PASSWORD|AIRLOCK_DB_PASSWORD|S3_ACCESS_KEY|S3_SECRET_KEY)=' "$SRC/.runtime/airlock/.env" > "$TMP/airlock.env"
chmod 600 "$TMP"/*.env
sync "$TMP/metrika.env" "$ROOT/secrets/metrika.env"
sync "$TMP/airlock.env" "$ROOT/secrets/airlock.env"
remote "chmod -R go-rwx '$ROOT/secrets'; find '$ROOT/secrets' -type d -exec chmod u+rwx,go-rwx {} +; find '$ROOT/secrets' -type f -exec chmod u+rw,go-rwx {} +"
dump(){
  local c="$1" u="$2" mode="$3" p="$4" q db safe
  if [[ "$mode" == peer ]]; then
    q=$("$D" --context "$CTX" exec -u postgres "$c" psql -At -U postgres -d postgres -c "select datname from pg_database where datistemplate=false")
  elif [[ "$mode" == secret ]]; then
    q=$("$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$(cat /run/secrets/db-password) psql -At -U '$u' -d postgres -c \"select datname from pg_database where datistemplate=false\"")
  else
    q=$("$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$POSTGRES_PASSWORD psql -At -U '$u' -d postgres -c \"select datname from pg_database where datistemplate=false\"")
  fi
  while IFS= read -r db; do
    [[ -z "$db" ]] && continue
    safe=$(printf '%s' "$db" | tr -c 'A-Za-z0-9_.-' '_')
    log "pg_dump $p/$db"
    if [[ "$mode" == peer ]]; then "$D" --context "$CTX" exec -u postgres "$c" pg_dump -Fc -d "$db" > "$TMP/$p--$safe.dump"
    elif [[ "$mode" == secret ]]; then "$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$(cat /run/secrets/db-password) pg_dump -Fc -U '$u' -d '$db'" > "$TMP/$p--$safe.dump"
    else "$D" --context "$CTX" exec "$c" sh -c "PGPASSWORD=\$POSTGRES_PASSWORD pg_dump -Fc -U '$u' -d '$db'" > "$TMP/$p--$safe.dump"
    fi
    sync "$TMP/$p--$safe.dump" "$ROOT/dumps/$p--$safe.dump"
  done <<< "$q"
}
log "dumping Airlock, Redmine and Baserow databases"
dump aifence-postgres-1 airlock env airlock
dump aifence-personal-redmine-database-1 redmine secret redmine
dump aifence-baserow-baserow-1 postgres peer baserow
log "saving amd64 images to server"
mapfile -t LIVE < <("$D" --context "$CTX" ps -q --filter label=run.airlock.instance=aifence | xargs -r "$D" --context "$CTX" inspect --format '{{.Config.Image}}' | sort -u)
IMAGES=(aifence-airlock:personal-624cc8d53339 aifence-frontend:personal-624cc8d53339 aifence-agent-builder:be0f9ee03be3 aifence-agent-base:be0f9ee03be3 aifence-js-executor:be0f9ee03be3 pgvector/pgvector:pg17 rustfs/rustfs:latest postgres:16-alpine redmine:6.1.6-alpine3.23 curlimages/curl:latest "${LIVE[@]}")
mapfile -t IMAGES < <(printf '%s\n' "${IMAGES[@]}"|sort -u); for i in "${IMAGES[@]}"; do "$D" --context "$CTX" image inspect "$i" >/dev/null || die "missing image $i"; done
save_group(){
  local image="$1" attempt
  if remote "docker image inspect '$image' >/dev/null 2>&1"; then
    log "image already present $image"
    return 0
  fi
  for attempt in 1 2 3 4 5; do
    log "saving image $image (attempt $attempt)"
    if "$D" --context "$CTX" save "$image" | gzip -1 | remote "gzip -d | docker load"; then
      if remote "docker image inspect '$image' >/dev/null 2>&1"; then
        return 0
      fi
    fi
    [[ "$attempt" -ge 5 ]] && break
    log "retrying image $image after transient transfer failure"
    sleep 5
  done
  die "image transfer failed: $image"
}
for image in "${IMAGES[@]}"; do save_group "$image"; done
remote "docker pull --platform linux/amd64 baserow/baserow:2.4.3 caddy:2-alpine"
log "copying live Docker volumes"
vol(){
  local n="$1" attempt
  for attempt in 1 2 3 4 5; do
    log "volume $n (attempt $attempt)"
    if "$D" --context "$CTX" run --rm -v "$n:/from:ro" caddy:2-alpine sh -c 'tar -C /from -czf - .' | remote "docker run --rm -i -v '$n:/to' caddy:2-alpine sh -c 'tar -C /to -xzf -'"; then
      return 0
    fi
    [[ "$attempt" -ge 5 ]] && break
    log "retrying volume $n after transient transfer failure"
    sleep 5
  done
  die "volume transfer failed: $n"
}
for v in aifence-data aifence_rustfsdata aifence_caddy-data aifence_caddy-config aifence-baserow_data aifence-personal-redmine_database aifence-personal-redmine_files userio-airlock-standalone_state; do vol "$v"; done
cat > "$TMP/.env" <<EOF
COMPOSE_PROJECT_NAME=aifence-move
TLS_MODE=proxy
COMPOSE_PROFILES=bundled-db,bundled-s3,caddy-proxy
AIRLOCK_INSTANCE_ID=aifence
DOCKER_NETWORK=aifence
AGENT_NETWORK=aifence-agents
AGENT_NETWORK_PER_AGENT=true
AGENT_HTTP_PRIVATE_CIDRS=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,fc00::/7
AGENT_CODEGEN_VOLUME=aifence-data
DOCKER_SOCKET_PATH=/var/run/docker.sock
DOMAIN=$HOST
PUBLIC_URL=https://$HOST
S3_URL_PUBLIC=https://s3.$HOST
PROXY_HTTP_PORT=$PORT
HTTP_PORT=$PORT
DOCKER_DEFAULT_PLATFORM=linux/amd64
AIRLOCK_IMAGE=aifence-airlock:personal-624cc8d53339
FRONTEND_IMAGE=aifence-frontend:personal-624cc8d53339
AGENT_BUILDER_IMAGE=aifence-agent-builder:be0f9ee03be3
AGENT_BASE_IMAGE=aifence-agent-base:be0f9ee03be3
JS_EXECUTOR_IMAGE=aifence-js-executor:be0f9ee03be3
EOF
cat "$TMP/.env" "$TMP/airlock.env" > "$TMP/.env.full"; sync "$TMP/.env.full" "$ROOT/airlock/.env"; remote "chmod 600 '$ROOT/airlock/.env'"
cat > "$TMP/compose.server100.yml" <<'EOF'
services:
  airlock: {platform: linux/amd64}
  frontend: {platform: linux/amd64}
  postgres: {platform: linux/amd64}
  postgres-bootstrap: {platform: linux/amd64}
  caddy-proxy: {platform: linux/amd64}
EOF
sync "$TMP/compose.server100.yml" "$ROOT/airlock/compose.server100.yml"
cat > "$TMP/redmine.server100.yml" <<'EOF'
services:
  database: {platform: linux/amd64}
  redmine:
    platform: linux/amd64
    volumes:
      - files:/usr/src/redmine/files
      - ./plugins/airlock_sso:/usr/src/redmine/plugins/airlock_sso
      - ./sso-public.pem:/run/airlock-sso/public.pem:ro
    secrets:
      - db-password
      - rails-secret
secrets:
  db-password: {file: ../secrets/redmine-db-password}
  rails-secret: {file: ../secrets/redmine-rails-secret}
EOF
sync "$TMP/redmine.server100.yml" "$ROOT/redmine/compose.server100.yml"
cat > "$TMP/baserow.server100.yml" <<'EOF'
services:
  baserow:
    platform: linux/amd64
EOF
sync "$TMP/baserow.server100.yml" "$ROOT/apps/baserow/compose.server100.yml"
cat > "$TMP/userio.server100.yml" <<'EOF'
services:
  userio:
    platform: linux/amd64
    image: userio-airlock:56c66260-mcp-oauth-amd64
  mcp-local: {platform: linux/amd64}
EOF
sync "$TMP/userio.server100.yml" "$ROOT/apps/userio/compose.server100.yml"
cat > "$TMP/redmine-mcp.server100.yml" <<'EOF'
services:
  redmine-mcp: {platform: linux/amd64}
EOF
sync "$TMP/redmine-mcp.server100.yml" "$ROOT/apps/redmine-mcp/compose.server100.yml"
cat > "$TMP/metrika.server100.yml" <<EOF
services:
  metrika-mcp:
    platform: linux/amd64
    image: aifence-metrika-metrika-mcp:server100
    network_mode: container:aifence-move-airlock-1
EOF
sync "$TMP/metrika.server100.yml" "$ROOT/apps/metrika/compose.server100.yml"
log "building arm64-only services for amd64"
remote "cd '$ROOT/apps/redmine-mcp' && docker build --platform linux/amd64 -t aifence-redmine-mcp:2.23.0 ."
remote "cd '$ROOT/apps/metrika' && docker build --platform linux/amd64 -t aifence-metrika-metrika-mcp:server100 ."
remote "cd '$ROOT/apps/userio' && docker build --platform linux/amd64 -t userio-airlock:56c66260-mcp-oauth-amd64 --build-arg USERIO_REVISION=56c66260f2c1bf75c4b615e369fd0e032184e0ee -f source/Dockerfile source"
log "starting core, apps and adapters"
remote "cd '$ROOT/airlock' && docker compose --env-file .env -f docker-compose.yml -f compose.server100.yml up -d --no-build --pull never"
remote "cd '$ROOT/redmine' && docker compose -p aifence-move-redmine -f compose.yaml -f compose.server100.yml up -d --no-build --pull never"
remote "docker network inspect aifence-agent-net-37aa7358-942e-44c4-acd0-b971e83e0ce9 >/dev/null 2>&1 || docker network create aifence-agent-net-37aa7358-942e-44c4-acd0-b971e83e0ce9"
remote "cd '$ROOT/apps/baserow' && docker compose -p aifence-move-baserow -f compose.yml -f compose.server100.yml up -d --no-build --pull never"
remote "cd '$ROOT/apps/redmine-mcp' && docker compose -p aifence-move-redmine-mcp -f compose.yaml -f compose.server100.yml up -d --no-build --pull never"
remote "cd '$ROOT/apps/metrika' && docker compose -p aifence-move-metrika --env-file '$ROOT/secrets/metrika.env' -f metrika.compose.yml -f compose.server100.yml up -d --no-build --pull never"
remote "cd '$ROOT/apps/userio' && docker compose -p userio-airlock-standalone-move -f compose.yaml -f compose.server100.yml up -d --no-build --pull never"
log "installing isolated HTTPS edge route"
cat > "$TMP/nginx.conf" <<EOF
server {
    listen 127.0.0.1:8444 ssl;
    server_name $HOST *.aifence.bezrabotnyi.com;
    ssl_certificate /etc/letsencrypt/live/aifence.bezrabotnyi.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/aifence.bezrabotnyi.com/privkey.pem;
    client_max_body_size 64m;
    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        proxy_buffering off;
    }
}
EOF
cat "$TMP/nginx.conf" | remote "sudo tee /etc/nginx/sites-available/aifence-move.bezrabotnyi.com >/dev/null && sudo ln -sfn /etc/nginx/sites-available/aifence-move.bezrabotnyi.com /etc/nginx/sites-enabled/aifence-move.bezrabotnyi.com && sudo nginx -t && sudo systemctl reload nginx"
log "health checks"
remote "curl -kfsS --max-time 20 -H 'Host: $HOST' https://127.0.0.1/health >/dev/null"
remote "curl -kfsS --max-time 20 -H 'Host: $HOST' https://127.0.0.1/ >/dev/null"
remote "docker ps --format '{{.Names}}|{{.Status}}' | grep -E 'aifence-move-airlock-1|aifence-move-frontend-1|aifence-move-redmine|aifence-move-baserow|aifence-move-metrika|userio-airlock-standalone-move'"
remote "cat > '$ROOT/MOVE_RECEIPT' <<EOF
source_context=$CTX
source_revision=$("$D" --context "$CTX" inspect aifence-airlock-1 --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
timestamp_utc=$(date -u +%Y%m%dT%H%M%SZ)
public_host=$HOST
caddy_loopback_port=$PORT
EOF"
log "complete: https://$HOST"

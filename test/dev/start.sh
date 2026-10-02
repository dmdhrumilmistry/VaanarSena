#!/usr/bin/env bash
# Local dev server for working on the console and the API.
#
#   test/dev/start.sh            # then open https://localhost:18443
#
# HTTPS on :18443 with a self-signed certificate, PostgreSQL in a Docker
# container named vs-dev-pg (port 55431, kept between runs), the console served
# from internal/web/static on disk (edit a file and reload, no rebuild), and the
# example manifests applied as the "gitops" owner. Go changes need a restart.
# State lives in test/dev/.run (git-ignored). Sign in with admin@example.com and
# VS_DEV_PASSWORD (default correct-horse-battery). Dev use only.
set -euo pipefail
export MSYS_NO_PATHCONV=1
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
run="$here/.run"
mkdir -p "$run"
# Native Windows tools (openssl, the server) need Windows paths.
hostpath() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
R=$(hostpath "$run")

pg=${VS_DEV_PG_CONTAINER:-vs-dev-pg}
pgport=${VS_DEV_PG_PORT:-55431}
if ! docker ps --format '{{.Names}}' | grep -qx "$pg"; then
  docker start "$pg" >/dev/null 2>&1 ||
    docker run -d --name "$pg" -e POSTGRES_USER=vs -e POSTGRES_PASSWORD=vs -e POSTGRES_DB=vs_dev \
      -p "$pgport:5432" postgres:17-alpine >/dev/null
  until docker exec "$pg" pg_isready -U vs >/dev/null 2>&1; do sleep 1; done
  sleep 1
fi

if [ ! -f "$run/tls.crt" ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 365 -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" -keyout "$R/tls.key" -out "$R/tls.crt" 2>/dev/null
fi

bin="$run/vaanarsena$(go env GOEXE)"
(cd "$repo" && go build -o "$(hostpath "$bin")" ./cmd/vaanarsena)

export VS_DATABASE_URL="postgres://vs:vs@localhost:$pgport/vs_dev?sslmode=disable"
export VS_SECRET_KEY=${VS_SECRET_KEY:-dev-only-0123456789abcdef0123456789abcdef}
export VS_LISTEN_ADDR=:18443 VS_PUBLIC_URL=https://localhost:18443
export VS_TLS_CERT_FILE="$R/tls.crt" VS_TLS_KEY_FILE="$R/tls.key"
export VS_BOOTSTRAP_ADMIN_EMAIL=admin@example.com VS_BOOTSTRAP_ADMIN_PASSWORD=${VS_DEV_PASSWORD:-correct-horse-battery}
export VS_ORG_NAME="Acme Labs" VS_MANIFEST_DIR="$(hostpath "$repo")/examples/manifests" VS_MANIFEST_OWNER=gitops
export VS_WEB_DIR="$(hostpath "$repo")/internal/web/static"
exec "$bin" serve

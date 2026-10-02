#!/usr/bin/env bash
# End-to-end test of the Docker image with real Linux agents.
#
#   test/docker/agents.sh          # build, test, clean up
#   KEEP=1 test/docker/agents.sh   # leave the stack running for a look
#
# Builds the image from this checkout, starts deploy/docker-compose.yml as the
# compose project "vs-dockertest" (HTTPS on https://localhost:18843, server
# hostname "vs"), then enrolls the real vaanarsena-agent from the image in four
# containers on the compose network:
#
#   vs-dt-ubuntu    ubuntu:24.04       corporate  dpkg
#   vs-dt-arch      archlinux:latest   corporate  pacman
#   vs-dt-alma      almalinux/9-init   corporate  rpm, systemd as PID 1 (privileged)
#   vs-dt-personal  ubuntu:24.04       personal   must store no software
#
# and checks the software inventory the server stored against each distro's own
# package tools and systemd. Cleanup removes only these containers and the
# compose project (with its volume); images stay cached. Needs Docker, curl,
# openssl and python3.
set -euo pipefail
export MSYS_NO_PATHCONV=1
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
run="$here/.run"
mkdir -p "$run/tls"
project=vs-dockertest
agents=(ubuntu arch alma personal)

# Docker on Windows wants Windows paths in bind mounts.
hostpath() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
R=$(hostpath "$run")

# python3 where it is real; on Windows that name can be a store stub.
PY=python3
"$PY" -c 'import sys' >/dev/null 2>&1 || PY=python

compose() {
  docker compose -p "$project" -f "$(hostpath "$repo")/deploy/docker-compose.yml" -f "$R/override.yml" --env-file "$R/.env" "$@"
}

# Removes only what this script creates.
teardown() {
  for a in "${agents[@]}"; do docker rm -f "vs-dt-$a" >/dev/null 2>&1 || true; done
  compose down -v >/dev/null 2>&1 || true
}
on_exit() {
  if [ "${KEEP:-}" = 1 ]; then
    echo "KEEP=1: stack left running at https://localhost:18843 (credentials in $run/.env)"
  else
    teardown
  fi
}

echo "== build image"
docker build -q -t vaanarsena:dockertest --build-arg VERSION=dockertest "$(hostpath "$repo")" >/dev/null

if [ ! -f "$run/tls/tls.crt" ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 -subj "/CN=vs" \
    -addext "subjectAltName=DNS:vs,DNS:localhost,IP:127.0.0.1" \
    -keyout "$R/tls/tls.key" -out "$R/tls/tls.crt" 2>/dev/null
  chmod 644 "$run/tls/tls.key" # read by the non-root server user
fi
cat > "$run/.env" <<EOF
POSTGRES_PASSWORD=$(openssl rand -hex 12)
VS_SECRET_KEY=$(openssl rand -hex 32)
VS_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
VS_BOOTSTRAP_ADMIN_PASSWORD=docker-test-$(openssl rand -hex 8)
VS_PUBLIC_URL=https://vs:8080
VS_ORG_NAME=Docker test
VS_IMAGE=vaanarsena:dockertest
EOF
cat > "$run/override.yml" <<EOF
services:
  vaanarsena:
    hostname: vs
    ports: !override
      - "18843:8080"
    environment:
      VS_TLS_CERT_FILE: /tls/tls.crt
      VS_TLS_KEY_FILE: /tls/tls.key
    volumes:
      - "$R/tls:/tls:ro"
EOF

echo "== start stack"
teardown # start from a clean project
trap on_exit EXIT
compose up -d >/dev/null 2>&1
for _ in $(seq 60); do curl -sk https://localhost:18843/healthz | grep -q ok && break; sleep 1; done
curl -sk https://localhost:18843/healthz | grep -q ok || { docker logs "$project-vaanarsena-1" | tail -20; exit 1; }

id=$(docker create vaanarsena:dockertest)
docker cp "$id:/usr/local/share/vaanarsena/vaanarsena-agent" "$R/vaanarsena-agent" >/dev/null
docker rm "$id" >/dev/null

echo "== enrollment tokens"
"$PY" - "$R" <<'EOF'
import json, ssl, sys, urllib.request
run = sys.argv[1]
env = dict(l.strip().split("=", 1) for l in open(run + "/.env") if "=" in l)
ctx = ssl._create_unverified_context()
def call(m, p, b=None, tok=None):
    h = {"Content-Type": "application/json", "X-Requested-With": "test"}
    if tok: h["Authorization"] = "Bearer " + tok
    req = urllib.request.Request("https://localhost:18843/api/v1" + p, data=json.dumps(b).encode() if b is not None else None, headers=h, method=m)
    return json.loads(urllib.request.urlopen(req, context=ctx).read() or b"{}")
tok = call("POST", "/auth/login", {"email": "admin@example.com", "password": env["VS_BOOTSTRAP_ADMIN_PASSWORD"]})["token"]
open(run + "/api.token", "w").write(tok)
for name, own in [("ubuntu", "corporate"), ("arch", "corporate"), ("alma", "corporate"), ("personal", "personal")]:
    t = call("POST", "/enrollment-tokens", {"platform": "linux", "ownership": own, "assignee": name + "@example.com"}, tok)["token"]
    open(run + "/" + name + ".enroll", "w").write(t)
EOF

echo "== agents"
net="${project}_default"
mounts=(-v "$R/vaanarsena-agent:/usr/local/bin/vaanarsena-agent:ro" -v "$R/tls/tls.crt:/ca.crt:ro")
enroll_cmd() { echo "vaanarsena-agent enroll --server https://vs:8080 --token $(cat "$run/$1.enroll") --server-ca /ca.crt"; }
for pair in ubuntu:ubuntu:24.04 arch:archlinux:latest personal:ubuntu:24.04; do
  name=${pair%%:*}; image=${pair#*:}
  docker run -d --name "vs-dt-$name" --hostname "vs-dt-$name" --network "$net" "${mounts[@]}" "$image" \
    sh -c "$(enroll_cmd "$name") && exec vaanarsena-agent run" >/dev/null
done
docker run -d --name vs-dt-alma --hostname vs-dt-alma --network "$net" --privileged --cgroupns=host \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw "${mounts[@]}" almalinux/9-init >/dev/null
for _ in $(seq 60); do
  state=$(docker exec vs-dt-alma systemctl is-system-running 2>/dev/null || true)
  case "$state" in running|degraded) break ;; esac
  sleep 1
done
docker exec -d vs-dt-alma sh -c "$(enroll_cmd alma) > /tmp/enroll.log 2>&1 && exec vaanarsena-agent run > /tmp/agent.log 2>&1"

# What each distro says is installed, to compare with what the server stored.
docker exec vs-dt-ubuntu sh -c "dpkg-query -W -f '\${db:Status-Abbrev}\n' | grep -c '^ii'" > "$run/expect.ubuntu"
docker exec vs-dt-arch sh -c 'pacman -Q 2>/dev/null | wc -l' > "$run/expect.arch"
docker exec vs-dt-alma sh -c "rpm -qa --qf '%{NAME}\n' | grep -vc '^gpg-pubkey$'" > "$run/expect.alma"
docker exec vs-dt-alma sh -c "systemctl list-units --type=service --all --no-legend --plain | grep -vc ' not-found '" > "$run/expect.alma.services"

echo "== wait for inventory and verify"
"$PY" - "$R" <<'EOF'
import json, ssl, sys, time, urllib.request
run = sys.argv[1]
ctx = ssl._create_unverified_context()
tok = open(run + "/api.token").read()
def get(p):
    req = urllib.request.Request("https://localhost:18843/api/v1" + p, headers={"Authorization": "Bearer " + tok, "X-Requested-With": "test"})
    return json.loads(urllib.request.urlopen(req, context=ctx).read())
expect = {n: int(open(f"{run}/expect.{n}").read().strip()) for n in ("ubuntu", "arch", "alma")}
expect_services = int(open(run + "/expect.alma.services").read().strip())
deadline = time.time() + 240
while True:
    devs = {d["name"]: d for d in get("/devices?limit=50")["devices"]}
    inv = {}
    for n in ("ubuntu", "arch", "alma", "personal"):
        d = devs.get("vs-dt-" + n)
        inv[n] = get(f"/devices/{d['id']}/inventory?kind=app&limit=1") if d else None
    if all(inv[n] and inv[n]["counts"]["app"] for n in ("ubuntu", "arch", "alma")) and inv["personal"]:
        break
    if time.time() > deadline:
        sys.exit("FAIL: inventory did not arrive within 240 s: " + json.dumps({k: v and v["counts"] for k, v in inv.items()}))
    time.sleep(3)
fails = []
def check(ok, msg):
    print(("ok   " if ok else "FAIL ") + msg)
    if not ok: fails.append(msg)
for n, src in (("ubuntu", "dpkg"), ("arch", "pacman"), ("alma", "rpm")):
    got = inv[n]["counts"]["app"]
    check(got == expect[n], f"{n}: {got} apps stored, {src} reports {expect[n]}")
alma = devs["vs-dt-alma"]["id"]
svc = get(f"/devices/{alma}/inventory?kind=service&limit=1000")
check(svc["total"] == expect_services, f"alma: {svc['total']} services stored, systemd lists {expect_services}")
check(any(i["state"] == "running" for i in svc["items"]), "alma: running services reported")
p = inv["personal"]
check(p["counts"] == {"app": 0, "profile": 0, "service": 0} and not any(p["supported"].values()) and p["note"],
      "personal: nothing stored, nothing supported, note shown")
fleet = get("/inventory/software?q=bash")
row = next((i for i in fleet["items"] if i["name"] == "bash"), None)
check(row is not None and row["devices"] == 3 and sorted(row["sources"]) == ["dpkg", "pacman", "rpm"],
      "fleet: bash is one row across dpkg, pacman and rpm on 3 devices")
stats = get("/stats")["software"]
check(stats["devicesReporting"] == 3, f"stats: {stats['devicesReporting']} devices reporting software")
sys.exit(1 if fails else 0)
EOF
echo "== passed"

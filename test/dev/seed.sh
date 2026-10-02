#!/usr/bin/env bash
# Seed the dev server (test/dev/start.sh) with six Linux "devices" that speak
# the real agent protocol (enroll + mTLS check-in) so every view has realistic
# data, five corporate and one personal. Run once per fresh database, then
# python test/dev/seed-inventory.py for their software.
set -euo pipefail
export MSYS_NO_PATHCONV=1
here=$(cd "$(dirname "$0")" && pwd)
# Native Windows tools (python) need Windows paths.
hostpath() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else printf '%s' "$1"; fi; }
checkin="$(hostpath "$here")/checkin.py"
mkdir -p "$here/.run" && cd "$here/.run"
B=${VS_DEV_BASE:-https://localhost:18443}
PW=${VS_DEV_PASSWORD:-correct-horse-battery}
CURL="curl -sk"
JWT=$($CURL -X POST $B/api/v1/auth/login -H 'Content-Type: application/json' \
  -d "{\"email\":\"admin@example.com\",\"password\":\"$PW\"}" | python -c 'import sys,json;print(json.load(sys.stdin)["token"])')
api() { $CURL -X "$1" "$B/api/v1$2" -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' ${3:+-d "$3"}; }

enroll() { # name ownership assignee encrypted osPretty model
  local name=$1 own=$2 who=$3 enc=$4 os=$5 model=$6 d=dev-$1
  mkdir -p "$d"
  local tok; tok=$(api POST /enrollment-tokens "{\"platform\":\"linux\",\"ownership\":\"$own\",\"assignee\":\"$who\"}" | python -c 'import sys,json;print(json.load(sys.stdin)["token"])')
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=$name" -keyout "$d/key.pem" -outform DER -out "$d/csr.der" 2>/dev/null
  local csr; csr=$(base64 -w0 "$d/csr.der")
  $CURL -X POST $B/agent/v1/enroll -H 'Content-Type: application/json' \
    -d "{\"token\":\"$tok\",\"csr\":\"$csr\",\"machineId\":\"mid-$name\",\"hostname\":\"$name\",\"os\":\"Ubuntu\",\"osVersion\":\"24.04\"}" \
    | python -c 'import sys,json;open(sys.argv[1],"w").write(json.load(sys.stdin)["certificate"])' "$d/cert.pem"
  local issues='[]'
  [ "$enc" = true ] || issues='["disk encryption required but no LUKS volume found"]'
  local body="{\"facts\":{\"hostname\":\"$name\",\"osPretty\":\"$os\",\"model\":\"$model\",\"serial\":\"SN-$name\",\"diskEncrypted\":$enc,\"firewall\":true,\"kernel\":\"6.8.0\"},\"compliance\":{\"compliant\":$enc,\"issues\":$issues}}"
  python "$checkin" "$d" "$body"
  python "$checkin" "$d" "$body"
  echo "enrolled $name"
}
enroll build-01 corporate devops@example.com true "Ubuntu 24.04.1 LTS" "Dell PowerEdge R650"
enroll design-lap-07 corporate priya@example.com false "Fedora Linux 41" "Lenovo ThinkPad X1 Carbon"
enroll eng-ws-12 corporate arjun@example.com true "Debian GNU/Linux 12" "Framework Laptop 13"
enroll kiosk-lobby corporate "" true "Ubuntu 22.04.4 LTS" "Intel NUC 13"
enroll rahul-home corporate rahul@example.com false "Arch Linux" "ASUS Zenbook 14"
enroll meera-personal personal meera@example.com true "Ubuntu 24.04.1 LTS" "HP EliteBook 840"
api POST /groups '{"name":"Lab machines","kind":"static","description":"Bench and test rigs"}' >/dev/null
echo seeded

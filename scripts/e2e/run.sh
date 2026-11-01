#!/usr/bin/env bash
# Browser e2e: build rmfakecloud on the services VM, seed a fresh data dir,
# start it on :3099, run Playwright from here. Usage: scripts/e2e/run.sh [pytest args]
# E2E_KEEP=1 leaves the container and data dir up for debugging.
# Env passthrough to the container: RM_PAGES_URL, PAGEWATCH_MIN_AGE.
set -euo pipefail

HOST=${E2E_HOST:-mike@10.0.0.11}
REMOTE=/tmp/rmfc-e2e
DATA=/home/mike/rmfc-e2e-data
NAME=rmfc-e2e
PORT=3099
GO_IMG=docker.io/library/golang:bookworm
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

cleanup() {
  ssh "$HOST" "podman rm -f $NAME >/dev/null 2>&1; rm -rf $DATA $REMOTE" || true
}
[ -n "${E2E_KEEP:-}" ] || trap cleanup EXIT
trap "exit 130" INT
trap "exit 143" TERM
cleanup

echo ">> rsync + build image"
rsync -az --delete --exclude ui/node_modules --exclude .git "$ROOT"/ "$HOST:$REMOTE/"
ssh "$HOST" "cd $REMOTE && podman build -q --build-arg VERSION=e2e -t localhost/rmfakecloud:e2e -f Dockerfile ."

# env passthrough: expanded here (ssh does not forward env), quoted for the remote shell
ENVS=""
for v in RM_PAGES_URL PAGEWATCH_MIN_AGE; do
  [ -n "${!v:-}" ] && ENVS="$ENVS -e $(printf '%q' "$v=${!v}")"
done

echo ">> seed data dir"
ssh "$HOST" bash -s <<REMOTE_EOF
set -euo pipefail
mkdir -p $DATA
cd $REMOTE
mkdir -p ui/dist && { [ -f ui/dist/index.html ] || touch ui/dist/index.html; }
podman run --rm -v $REMOTE:/src:Z -v rmfc-gocache:/root/go -w /src -e CGO_ENABLED=0 $GO_IMG go build -o /src/rmseed ./cmd/rmseed
seed() { podman run --rm -v $REMOTE:/src:Z -v $DATA:/data:Z -w /src $GO_IMG /src/rmseed --data /data --user e2e "\$@"; }
T=internal/testkit/tablet/testdata
seed init
seed folder 11111111-1111-4111-8111-111111111111 Inbox ""
seed folder 22222222-2222-4222-8222-222222222222 Archive ""
seed folder 33333333-3333-4333-8333-333333333333 Projects ""
seed folder 44444444-4444-4444-8444-444444444444 Alpha 33333333-3333-4333-8333-333333333333
seed notebook 55555555-5555-4555-8555-555555555555 "E2E Notes" 33333333-3333-4333-8333-333333333333
seed page 55555555-5555-4555-8555-555555555555 p1 /src/\$T/strokes_a.rm
# password login e2e / e2epass123 (rmseed init uses a placeholder password)
podman run --rm -v $DATA:/data:Z -e DATADIR=/data localhost/rmfakecloud:e2e setuser -u e2e -p e2epass123 -a -s
podman run -d --name $NAME -p $PORT:3000 -v $DATA:/data:Z -e DATADIR=/data -e JWT_SECRET_KEY=e2e $ENVS localhost/rmfakecloud:e2e
REMOTE_EOF

BASE=${E2E_BASE_URL:-http://${HOST#*@}:$PORT}
echo ">> wait for $BASE"
up=""
for _ in $(seq 60); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/")" != 000 ] && { up=1; break; }
  sleep 1
done
[ -n "$up" ] || { echo "server not up after 60s: $BASE" >&2; ssh "$HOST" "podman logs --tail 50 $NAME" >&2 || true; exit 1; }

echo ">> pytest"
cd "$ROOT"
E2E_BASE_URL=$BASE RM_PAGES_URL=${RM_PAGES_URL:-} uv run --with pytest --with playwright python -m pytest scripts/e2e "$@"

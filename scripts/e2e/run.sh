#!/usr/bin/env bash
# Browser e2e: build rmfakecloud on the services VM, seed a fresh data dir,
# start it on :3099, run Playwright from here. Usage: scripts/e2e/run.sh [pytest args]
# E2E_KEEP=1 leaves the container and data dir up for debugging.
# Env passthrough to the container: RM_PAGES_URL, PAGEWATCH_MIN_AGE.
# E2E_STUB_RMPAGES (default 1) starts stub_rmpages.py on the services VM (:3098) and
# points the container's RM_PAGES_URL at it, so page watch is on (pipeline run-now tests).
# E2E_STUB_RMPAGES=0 opts out; setting RM_PAGES_URL without E2E_STUB_RMPAGES also means
# no stub (the real rm-pages). With the real rm-pages the rows of user e2e are deleted
# before and after the run (test_pipeline_e2e.py; see README).
set -euo pipefail

if [ -z "${E2E_STUB_RMPAGES:-}" ]; then
  if [ -n "${RM_PAGES_URL:-}" ]; then E2E_STUB_RMPAGES=0; else E2E_STUB_RMPAGES=1; fi
fi
export E2E_STUB_RMPAGES

HOST=${E2E_HOST:-mike@10.0.0.11}
REMOTE=/tmp/rmfc-e2e
DATA=/home/mike/rmfc-e2e-data
NAME=rmfc-e2e
STUB_PORT=3098
STUB_PID=/tmp/rmfc-e2e-stub.pid
PORT=3099
GO_IMG=docker.io/library/golang:bookworm
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

# Against the real rm-pages (RM_PAGES_URL set, no stub) user e2e leaves rows in its DB:
# delete them, strictly user e2e and its trace ids, before and after the run.
RMPAGES_CLEANUP_SQL="BEGIN;
CREATE TEMP TABLE e2e_traces ON COMMIT DROP AS SELECT trace_id FROM rm_pages.events WHERE user_id = 'e2e';
SELECT (SELECT count(*) FROM e2e_traces) AS e2e_events,
       (SELECT count(*) FROM rm_pages.llm_requests WHERE trace_id IN (SELECT trace_id FROM e2e_traces)) AS llm_calls,
       (SELECT COALESCE(sum(cost), 0) FROM rm_pages.llm_requests WHERE trace_id IN (SELECT trace_id FROM e2e_traces)) AS llm_cost_usd;
DELETE FROM rm_pages.trail WHERE trace_id IN (SELECT trace_id FROM e2e_traces);
DELETE FROM rm_pages.llm_requests WHERE trace_id IN (SELECT trace_id FROM e2e_traces);
DELETE FROM rm_pages.results WHERE trace_id IN (SELECT trace_id FROM e2e_traces);
DELETE FROM rm_pages.jobs WHERE trace_id IN (SELECT trace_id FROM e2e_traces);
DELETE FROM rm_pages.events WHERE user_id = 'e2e';
DELETE FROM rm_pages.contact WHERE user_id = 'e2e';
COMMIT;"

cleanup() {
  ssh "$HOST" "podman rm -f $NAME >/dev/null 2>&1; [ -f $STUB_PID ] && kill \$(cat $STUB_PID) 2>/dev/null; rm -rf $DATA $REMOTE $STUB_PID" || true
  if [ -n "${RM_PAGES_URL:-}" ] && [ "$E2E_STUB_RMPAGES" != 1 ]; then
    echo ">> delete rm-pages rows of user e2e"
    if ! ssh "$HOST" "podman exec -i personal-db psql -U personal -d personal -X -q -v ON_ERROR_STOP=1" <<<"$RMPAGES_CLEANUP_SQL"; then
      echo "WARNING: rm-pages cleanup of user e2e failed; rows of user e2e may remain in rm_pages.*" >&2
    fi
  fi
}
[ -n "${E2E_KEEP:-}" ] || trap cleanup EXIT
trap "exit 130" INT
trap "exit 143" TERM
cleanup

echo ">> rsync + build image"
rsync -az --delete --exclude ui/node_modules --exclude .git "$ROOT"/ "$HOST:$REMOTE/"
ssh "$HOST" "cd $REMOTE && podman build -q --build-arg VERSION=e2e -t localhost/rmfakecloud:e2e -f Dockerfile ."

if [ "$E2E_STUB_RMPAGES" = 1 ]; then
  echo ">> stub rm-pages on :$STUB_PORT"
  ssh "$HOST" "nohup python3 $REMOTE/scripts/e2e/stub_rmpages.py $STUB_PORT >/dev/null 2>&1 </dev/null & echo \$! > $STUB_PID"
  export RM_PAGES_URL=http://host.containers.internal:$STUB_PORT
fi

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
E2E_BASE_URL=$BASE RM_PAGES_URL=${RM_PAGES_URL:-} E2E_HOST=$HOST E2E_REMOTE=$REMOTE E2E_DATA=$DATA uv run --with pytest --with playwright python -m pytest scripts/e2e "$@"

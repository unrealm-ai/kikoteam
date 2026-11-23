#!/usr/bin/env bash
# Used only on the isolated Linux GitHub Actions runner with a disposable PostgreSQL service.
set -euo pipefail

for image in "$BACKEND_IMAGE_ID" "$HUB_IMAGE_ID" "$ENGINE_IMAGE_ID"; do
    [[ "$image" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo 'Expected an inspected local image ID'; exit 1; }
done
[[ "$POSTGRES_CONTAINER" =~ ^[0-9a-f]{64}$ ]]
db=image_smoke
backend_url=http://127.0.0.1:18080
hub_url=http://127.0.0.1:18081
backend=
hub=
engine=
storage=$(mktemp -d "$RUNNER_TEMP/kiko-smoke.XXXXXX")
storage_mount="type=bind,source=$storage,target=/mnt/kikoteam"
host_user="$(id -u):$(id -g)"

cleanup() {
    local status=$?
    trap - EXIT
    if (( status != 0 )); then
        [[ -z "$backend" ]] || docker logs --tail 100 "$backend" >&2 || true
        [[ -z "$hub" ]] || docker logs --tail 100 "$hub" >&2 || true
        [[ -z "$engine" ]] || docker logs --tail 100 "$engine" >&2 || true
    fi
    [[ -z "$engine" ]] || docker rm -f "$engine" >/dev/null || true
    [[ -z "$hub" ]] || docker rm -f "$hub" >/dev/null || true
    [[ -z "$backend" ]] || docker rm -f "$backend" >/dev/null || true
    rm -rf -- "$storage"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_until() {
    local label=$1
    shift
    for ((attempt = 0; attempt < 60; attempt++)); do
        "$@" && return 0
        sleep 2
    done
    printf 'Timed out waiting for %s\n' "$label" >&2
    return 1
}

http() { curl --fail --silent --max-time 3 "$1"; }
healthy_backend() { http "$backend_url/healthz" >/dev/null; }
ready_engine() { http http://127.0.0.1:18083/readyz >/dev/null; }
sql() {
    docker exec "$POSTGRES_CONTAINER" psql -XqAt -v ON_ERROR_STOP=1 -U postgres -d "$db" -c "$1"
}
ready_hub() {
    http "$hub_url/readyz" >/dev/null &&
        http "$hub_url/status" | jq -e '.phase == "running" and .workers_configured' >/dev/null
}

docker exec "$POSTGRES_CONTAINER" createdb -U postgres "$db"
dsn="postgres://postgres:test-only-password@127.0.0.1:5432/$db?sslmode=disable"
hub_token=kiko-acceptance-only-hub-token-not-a-secret
engine_token=kiko-acceptance-only-engine-token-not-a-secret
backend=$(docker run --detach --network host \
    --user "$host_user" --mount "$storage_mount" \
    -e "DATABASE_URL=$dsn" -e KIKO_AUTH_SECRET=kiko-acceptance-only-auth-not-a-secret \
    -e "KIKO_PUBLIC_ORIGIN=$backend_url" -e KIKO_HTTP_ADDRESS=127.0.0.1:18080 \
    -e KIKO_WEB_DIR=/app/web -e KIKO_STORAGE_LOCAL_ROOT=/mnt/kikoteam \
    -e "KIKO_MODEL_PROXY_ORIGIN=$backend_url" -e "KIKO_HUB_TOKEN=$hub_token" \
    -e KIKO_HUB_CONTROL_URL=http://127.0.0.1:18082 -e "KIKO_ENGINE_TOKEN=$engine_token" "$BACKEND_IMAGE_ID")
wait_until 'Backend HTTP liveness on the empty database' healthy_backend

hub=$(docker run --detach --network host \
    -e "EVENT_HUB_DATABASE_URL=$dsn" -e EVENT_HUB_HEALTH_ADDR=127.0.0.1:18081 \
    -e EVENT_HUB_CONTROL_ADDR=127.0.0.1:18082 -e "EVENT_HUB_BACKEND_URL=$backend_url" \
    -e "KIKO_HUB_TOKEN=$hub_token" "$HUB_IMAGE_ID")
wait_until 'Event Hub migration and active channel workers' ready_hub

# The packaged Hub embeds the migrations. Compare the entire applied set to the
# exact checkout without mounting migration files into the Hub container.
expected_versions=$(
    for migration in event-hub/database/postgres/migrations/*.sql; do
        name=${migration##*/}
        number=${name%%_*}
        [[ "$number" =~ ^[0-9]+$ ]] || exit 1
        printf '%d\n' "$((10#$number))"
    done | sort -n
)
actual_versions=$(sql 'SELECT version_id FROM public.goose_db_version WHERE is_applied AND version_id > 0 ORDER BY version_id')
diff -u <(printf '%s\n' "$expected_versions") <(printf '%s\n' "$actual_versions")

# Exercise both packaged static Web and the real Backend DB-backed auth options.
http "$backend_url/readyz" >/dev/null
http "$backend_url/" > "$RUNNER_TEMP/kiko-initial-page.html"
grep -qi '<html' "$RUNNER_TEMP/kiko-initial-page.html"
http "$backend_url/api/v1/auth/options" | jq -e '
    .password_registration_enabled == true
    and .email_code_enabled == false
    and .registration_verification_required == false
' >/dev/null

# Launch the packaged Engine normally, with the same asset volume privileges as
# Backend. Readiness means registration; SQL confirms Backend persisted it.
engine=$(docker run --detach --network host --init \
    --user "$host_user" --mount "$storage_mount" \
    -e "DATABASE_URL=$dsn" -e "KIKO_BACKEND_URL=$backend_url" \
    -e "KIKO_ENGINE_TOKEN=$engine_token" -e KIKO_ENGINE_ADDRESS=127.0.0.1:18083 \
    -e KIKO_ENGINE_HOST_ID=image-smoke -e KIKO_STORAGE_LOCAL_ROOT=/mnt/kikoteam "$ENGINE_IMAGE_ID")
wait_until 'Engine registration with the published Backend' ready_engine
registration=$(sql "SELECT EXISTS (SELECT 1 FROM public.manager_engines WHERE host_id='image-smoke' AND accepting)")
[[ "$registration" == t ]]

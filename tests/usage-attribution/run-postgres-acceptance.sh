#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
base_tmp_dir="${SUB2API_ATTRIBUTION_TMPDIR:-${TMPDIR:-/var/tmp}}"
scratch_dir="$(mktemp -d "${base_tmp_dir%/}/sub2api-attribution-XXXXXX")"
migration="$repo_root/backend/migrations/234_usage_attribution.sql"
container_name="sub2api-attribution-acceptance-$$"
postgres_image="${POSTGRES_IMAGE:-postgres:18-alpine}"

if [[ ! -f "$migration" ]]; then
  printf 'missing expected attribution migration: %s\n' "$migration" >&2
  exit 2
fi

mkdir -p "$scratch_dir"
chmod 700 "$scratch_dir"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -rf "$scratch_dir"
}
trap cleanup EXIT

docker run -d --rm --name "$container_name" --network none \
  -e POSTGRES_HOST_AUTH_METHOD=trust \
  -v "$repo_root:/repo:ro" \
  "$postgres_image" >/dev/null

for _ in $(seq 1 30); do
  if docker exec "$container_name" pg_isready -U postgres -d postgres >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$container_name" pg_isready -U postgres -d postgres >/dev/null

docker exec -i "$container_name" psql -v ON_ERROR_STOP=1 -U postgres -d postgres \
  < "$repo_root/tests/usage-attribution/postgres-contract.sql" \
  > "$scratch_dir/postgres-acceptance.log"
docker exec "$container_name" psql -v ON_ERROR_STOP=1 -U postgres -d postgres \
  -f /repo/backend/migrations/234_usage_attribution.sql \
  >> "$scratch_dir/postgres-acceptance.log"
docker exec -i "$container_name" psql -v ON_ERROR_STOP=1 -U postgres -d postgres \
  < "$repo_root/tests/usage-attribution/postgres-acceptance.sql" \
  >> "$scratch_dir/postgres-acceptance.log"

printf 'PASS isolated PostgreSQL attribution contract\n'

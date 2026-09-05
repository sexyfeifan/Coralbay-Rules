#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
source ./install.sh
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

for domain in rules.example.com a-b.example xn--fiqs8s.example; do
  valid_domain "$domain" || { echo "rejected valid domain: $domain"; exit 1; }
done
for domain in a..example .a.example -a.example a-.example http://a.example; do
  if valid_domain "$domain"; then echo "accepted invalid domain: $domain"; exit 1; fi
done

mkdir -p "$test_dir/env"
printf '%s\n' 'ADMIN_PASSWORD=Safe-password123' 'LOCAL_PORT=4999' > "$test_dir/env/.env"
load_env "$test_dir/env"
[[ "$ADMIN_PASSWORD" == Safe-password123 && "$LOCAL_PORT" == 4999 ]]
load_env "$test_dir/missing"
[[ -z "${ADMIN_PASSWORD:-}" && -z "${LOCAL_PORT:-}" ]]
printf 'ADMIN_PASSWORD=$(touch %s)\n' "$test_dir/executed" > "$test_dir/env/.env"
if (load_env "$test_dir/env") >/dev/null 2>&1; then echo 'unsafe env accepted'; exit 1; fi
[[ ! -e "$test_dir/executed" ]]

if (TTY_IN=/dev/null; prompt_required 'test') >/dev/null 2>&1; then echo 'EOF accepted'; exit 1; fi

# Check real rendering with Docker Compose when available; no daemon needed.
MIRROR_DOMAIN=rules.example.com; ADMIN_PASSWORD=Safe-password123
ADMIN_ACTION_TOKEN=0123456789abcdef; UPDATER_TOKEN=abcdef0123456789
write_env "$test_dir/env"
write_compose "$test_dir/env"
if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then
  docker compose --project-directory "$test_dir/env" -f "$test_dir/env/compose.yaml" --env-file "$test_dir/env/.env" config --quiet
fi

# Isolated Docker failures: preflight must never replace live config.
mkdir -p "$test_dir/live/data" "$test_dir/stage"
printf 'old-env\n' > "$test_dir/live/.env"
printf 'old-compose\n' > "$test_dir/live/compose.yaml"
printf 'new-env\n' > "$test_dir/stage/.env"
printf 'new-compose\n' > "$test_dir/stage/compose.yaml"
docker() { [[ "$*" != *pull* ]]; }
if (deploy_config "$test_dir/live" "$test_dir/stage") >/dev/null 2>&1; then echo 'pull failure ignored'; exit 1; fi
[[ "$(cat "$test_dir/live/.env")" == old-env && "$(cat "$test_dir/live/compose.yaml")" == old-compose ]]

# A start failure restores configuration, interval and persisted deadline.
mkdir -p "$test_dir/stage"
printf 'new-env\n' > "$test_dir/stage/.env"
printf 'new-compose\n' > "$test_dir/stage/compose.yaml"
printf 'old-settings\n' > "$test_dir/live/data/settings.json"
printf 'old-schedule\n' > "$test_dir/live/data/schedule.json"
docker() { [[ "$*" != *'up -d'* ]]; }
if (deploy_config "$test_dir/live" "$test_dir/stage" 3600) >/dev/null 2>&1; then echo 'start failure ignored'; exit 1; fi
[[ "$(cat "$test_dir/live/.env")" == old-env && "$(cat "$test_dir/live/compose.yaml")" == old-compose ]]
[[ "$(cat "$test_dir/live/data/settings.json")" == old-settings && "$(cat "$test_dir/live/data/schedule.json")" == old-schedule ]]

mkdir -p "$test_dir/stage"
printf 'new-env\n' > "$test_dir/stage/.env"
printf 'new-compose\n' > "$test_dir/stage/compose.yaml"
docker() { return 0; }
wait_for_service() { return 0; }
deploy_config "$test_dir/live" "$test_dir/stage" 3600 >/dev/null
[[ "$(cat "$test_dir/live/.env")" == new-env && "$(cat "$test_dir/live/compose.yaml")" == new-compose ]]
[[ "$(cat "$test_dir/live/data/settings.json")" == '{"interval_seconds":3600}' && ! -e "$test_dir/live/data/schedule.json" ]]
echo 'Installer regression checks passed'

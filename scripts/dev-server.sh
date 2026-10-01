#!/usr/bin/env bash
# Runs a local tt server for development (mise run server).
#
# Settings come from the environment; under mise, put them in the [env]
# section of mise.local.toml (gitignored; see mise.local.toml.example).
# Without TT_MASTER_KEY, the key is read from ~/.tt-master-key, created on
# first run, so keys keep working across restarts.
set -euo pipefail
cd "$(dirname "$0")/.."

export TT_DATABASE_URL="${TT_DATABASE_URL:-postgres://postgres:tt@localhost:55432/tt?sslmode=disable}"
export TT_BASE_URL="${TT_BASE_URL:-http://localhost:8080}"
addr="${TT_ADDR:-:8080}"

if [[ -z "${TT_MASTER_KEY:-}" ]]; then
  key_file="${TT_MASTER_KEY_FILE:-$HOME/.tt-master-key}"
  if [[ ! -f "$key_file" ]]; then
    (umask 077 && openssl rand -base64 32 > "$key_file")
    echo "dev-server: created a new master key in $key_file"
  fi
  export TT_MASTER_KEY="$(cat "$key_file")"
fi

# With the dev Postgres from `mise run pg:up`, start it and create the
# database if needed. Other Postgres URLs are used as given.
if [[ "$TT_DATABASE_URL" == *"@localhost:55432/"* ]]; then
  if [[ -z "$(docker ps -q -f name='^tt-pg$')" ]]; then
    mise run pg:up
  fi
  db="${TT_DATABASE_URL##*/}"
  db="${db%%\?*}"
  if [[ -z "$(docker exec tt-pg psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '$db'")" ]]; then
    docker exec tt-pg psql -U postgres -qc "CREATE DATABASE \"$db\""
    echo "dev-server: created database $db"
  fi
fi

if [[ -z "${TT_GITHUB_CLIENT_ID:-}" ]]; then
  echo "dev-server: GitHub login is off (set TT_GITHUB_CLIENT_ID/SECRET in mise.local.toml)"
fi
echo "dev-server: $TT_BASE_URL (database ${TT_DATABASE_URL%%\?*})"
exec ./tt server --addr "$addr"

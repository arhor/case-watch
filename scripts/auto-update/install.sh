#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
expected_dir="$HOME/case-watch"

if [[ "$repo_dir" != "$expected_dir" ]]; then
    echo "Clone the repository to $expected_dir before installing the updater." >&2
    exit 1
fi

if [[ ! -f "$repo_dir/.env" ]]; then
    echo "Create and configure $repo_dir/.env before installing." >&2
    exit 1
fi

for command in git docker systemctl; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "Missing required command: $command" >&2
        exit 1
    fi
done

docker compose version >/dev/null
docker info >/dev/null

systemctl --user link \
    "$repo_dir/scripts/auto-update/case-watch-update.service" \
    "$repo_dir/scripts/auto-update/case-watch-update.timer"
systemctl --user daemon-reload
systemctl --user enable --now case-watch-update.timer
systemctl --user start case-watch-update.service

echo "Auto-update installed. Check: systemctl --user status case-watch-update.timer"

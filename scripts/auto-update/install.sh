#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

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

systemctl --user disable --now case-watch-update.timer 2>/dev/null || true
"$repo_dir/scripts/auto-update/configure-units.sh"

# Remove the old indirection if it was created by a previous installer.
old_link="$HOME/.local/bin/case-watch-update"
if [[ -L "$old_link" && "$(readlink -f "$old_link")" == "$repo_dir/scripts/auto-update/update.sh" ]]; then
    rm "$old_link"
fi

systemctl --user enable --now case-watch-update.timer
systemctl --user start case-watch-update.service

echo "Auto-update installed from $repo_dir."
echo "Check: systemctl --user status case-watch-update.timer"

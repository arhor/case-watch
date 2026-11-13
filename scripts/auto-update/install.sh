#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ ! -f "$repo_dir/.env" ]]; then
    echo "Create and configure $repo_dir/.env before installing." >&2
    exit 1
fi

for command in git docker systemctl readlink; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "Missing required command: $command" >&2
        exit 1
    fi
done

docker compose version >/dev/null
docker info >/dev/null

unit_dir="$HOME/.config/systemd/user"
bin_dir="$HOME/.local/bin"
install -d "$unit_dir" "$bin_dir"

# A stable entry point lets the systemd unit work with repositories anywhere.
ln -sfn "$repo_dir/scripts/auto-update/update.sh" "$bin_dir/case-watch-update"

# Install units instead of linking to the checkout; reinstalling also handles
# installations previously linked to a different repository location.
systemctl --user disable --now case-watch-update.timer 2>/dev/null || true
rm -f "$unit_dir/case-watch-update.service" "$unit_dir/case-watch-update.timer"
install -m 0644 "$repo_dir/scripts/auto-update/case-watch-update.service" "$unit_dir/"
install -m 0644 "$repo_dir/scripts/auto-update/case-watch-update.timer" "$unit_dir/"

systemctl --user daemon-reload
systemctl --user enable --now case-watch-update.timer
systemctl --user start case-watch-update.service

echo "Auto-update installed from $repo_dir."
echo "Check: systemctl --user status case-watch-update.timer"

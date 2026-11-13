#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
unit_dir="$HOME/.config/systemd/user"

# systemd treats percent signs as specifier escapes in unit files.
if [[ "$repo_dir" == *%* || "$repo_dir" == *$'\n'* || "$repo_dir" == *'"'* || "$repo_dir" == *'\\'* ]]; then
    echo "Repository path contains characters unsupported by systemd unit generation." >&2
    exit 1
fi

install -d "$unit_dir"
template="$(cat "$repo_dir/scripts/auto-update/case-watch-update.service.in")"
printf '%s\n' "${template//@REPO_DIR@/$repo_dir}" > "$unit_dir/case-watch-update.service"
install -m 0644 "$repo_dir/scripts/auto-update/case-watch-update.timer" "$unit_dir/case-watch-update.timer"
systemctl --user daemon-reload

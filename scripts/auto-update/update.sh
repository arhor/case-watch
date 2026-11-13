#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_dir"

if [[ "$(git branch --show-current)" != "master" ]]; then
    echo "Auto-update requires the local master branch." >&2
    exit 1
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
    echo "Tracked files have local changes; refusing to update." >&2
    exit 1
fi

git fetch origin master
git merge --ff-only origin/master

current="$(git rev-parse HEAD)"
marker="$(git rev-parse --git-path case-watch-deployed)"
previous="$(cat "$marker" 2>/dev/null || true)"

if [[ "$current" == "$previous" ]]; then
    exit 0
fi

# Linked systemd units may have changed with the repository.
systemctl --user daemon-reload
docker compose up -d --build

# Only mark a commit as deployed after Compose completes successfully.
printf '%s\n' "$current" > "$marker"

#!/usr/bin/env bash
set -euo pipefail

# Configuration
# ------------------------------------------------------------

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Functions
# ------------------------------------------------------------

validate_repository() {
    if [[ "$(git branch --show-current)" != "master" ]]; then
        echo "Auto-update requires the local master branch." >&2
        exit 1
    fi

    if ! git diff --quiet || ! git diff --cached --quiet; then
        echo "Tracked files have local changes; refusing to update." >&2
        exit 1
    fi
}

sync_repository() {
    git fetch origin master
    git merge --ff-only origin/master
}

deploy() {
    # Linked systemd units may have changed with the repository.
    systemctl --user daemon-reload
    docker compose up -d --build
}

# Main
# ------------------------------------------------------------

main() {
    cd "$repo_dir"

    validate_repository
    sync_repository

    local current marker previous
    current="$(git rev-parse HEAD)"
    marker="$(git rev-parse --git-path case-watch-deployed)"
    previous="$(cat "$marker" 2>/dev/null || true)"

    if [[ "$current" == "$previous" ]]; then
        return 0
    fi

    deploy

    # Only mark a commit as deployed after Compose completes successfully.
    printf '%s\n' "$current" > "$marker"
}

main "$@"

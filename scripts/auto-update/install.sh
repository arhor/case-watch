#!/usr/bin/env bash
set -euo pipefail

# -- Configuration ------------------------------------------------------------

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
config_dir="$HOME/.config/case-watch"
unit_dir="$HOME/.config/systemd/user"

# -- Functions ----------------------------------------------------------------

validate_environment() {
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
}

configure_environment() {
    install -d "$config_dir" "$unit_dir"

    # EnvironmentFile uses systemd quoting, not shell quoting.
    local escaped_dir="${repo_dir//\\/\\\\}"
    escaped_dir="${escaped_dir//\"/\\\"}"
    printf 'REPO_DIR="%s"\n' "$escaped_dir" > "$config_dir/deploy.env"
}

install_units() {
    systemctl --user disable --now case-watch-update.timer 2>/dev/null || true

    # Replace any formerly generated units with links to the repository.
    rm -f "$unit_dir/case-watch-update.service" "$unit_dir/case-watch-update.timer"
    systemctl --user daemon-reload
    systemctl --user link "$repo_dir/scripts/auto-update/case-watch-update.service" "$repo_dir/scripts/auto-update/case-watch-update.timer"
    systemctl --user daemon-reload
    systemctl --user enable --now case-watch-update.timer
    systemctl --user start case-watch-update.service
}

# -- Main ---------------------------------------------------------------------

main() {
    validate_environment
    configure_environment
    install_units

    echo "Auto-update installed from $repo_dir."
    echo "Check: systemctl --user status case-watch-update.timer"
}

main "$@"

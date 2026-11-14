# Automatic updates

The Raspberry Pi polls `origin/master` every five minutes over an outgoing
connection. When the remote branch moves forward, it fast-forwards the local
checkout, rebuilds the image using the repository's `compose.yaml`, and
recreates the container with Docker Compose. No public IP, inbound ports,
GitHub webhook, registry, or GitHub Actions runner is required.

## Prerequisites

- Linux with a working **systemd user manager**, Git, Docker Engine, and the
  Docker Compose plugin.
- Docker access **without sudo** for the user running the updater. Membership
  in the `docker` group effectively grants root-level privileges.
- An existing clean checkout on branch `master`, in **any directory**.
- Internet access to GitHub for `git fetch` and to fetch Docker build
  dependencies.

## Set up

From an existing checkout:

```sh
cd /path/to/case-watch
git pull --ff-only
# Create .env if it does not already exist:
# cp .env.example .env
# Edit .env and configure the real Telegram bot token.
sudo loginctl enable-linger "$USER"
./scripts/auto-update/install.sh
```

On a new machine, clone the repository anywhere you like and follow the same
steps. `enable-linger` keeps user services running after logout and allows
them to start at boot. Run the installer as the regular user, not with sudo.

The installer writes the checkout location into
`~/.config/case-watch/deploy.env`, links the versioned systemd service
and timer directly from the repository, enables the timer, and starts
the initial update. There are no generated unit templates or executable
symlinks under `~/.local/bin`. Re-run the installer after moving the
repository to update its location.

## Operate

```sh
systemctl --user list-timers
systemctl --user status case-watch-update.timer
journalctl --user -u case-watch-update.service -n 50 --no-pager
systemctl --user start case-watch-update.service  # Check immediately
systemctl --user disable --now case-watch-update.timer  # Stop polling
```

The update script checks for a clean tracked worktree and only fast-forwards
`master`. It remembers the last successfully deployed commit in Git's
private directory, so a failed Compose command is retried on the next tick.
The marker records Compose command success, not application health. Docker
Compose retains the named `case-watch-data` volume on container recreation.

Changes to linked systemd units are reloaded automatically after a new
commit is fetched. Local changes to tracked files block updates; keep
machine-specific settings in the ignored `.env` file. If Docker is
unavailable, or the build fails, inspect the journal and correct the issue.

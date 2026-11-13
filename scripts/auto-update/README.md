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

The installer renders `case-watch-update.service.in` with the checkout's
absolute path and installs the resulting service and timer into
`~/.config/systemd/user/`. No `~/.local/bin` symlink is needed. The
installer enables the timer and starts the initial update. Re-run it after
moving the repository to another directory, without cloning again.

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

Changes to the systemd unit templates are rendered and reloaded automatically
when a new commit is deployed. Local changes to tracked files block updates;
keep machine-specific settings in the ignored `.env` file. If Docker is
unavailable, or the build fails, inspect the journal and correct the issue.

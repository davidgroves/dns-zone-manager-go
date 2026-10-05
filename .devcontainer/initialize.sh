#!/usr/bin/env bash
# Runs on the host before VS Code starts the devcontainer stack.
# 1. Syncs the Docker Desktop Linux VM clock.
# 2. Removes any containers with stale network references.
set -euo pipefail

# On macOS, Docker Desktop runs containers inside a Linux VM whose clock can
# drift after sleep/wake cycles. When the clock is skewed, apt GPG signature
# verification fails for every repository simultaneously, preventing the image
# from building at all. Syncing the VM's software clock from the hypervisor
# hardware clock (which tracks the macOS system time) before the build starts
# fixes this. `hwclock -s` requires a privileged container; it is a no-op on
# Linux Docker Engine where the clock is already correct.
echo "Syncing Docker VM clock..."
if docker run --rm --privileged alpine hwclock -s 2>/dev/null; then
    echo "Docker VM clock synced."
else
    echo "Clock sync skipped (not Docker Desktop, or insufficient privileges)."
fi

# Remove any Compose containers that reference a network that no longer exists.
bash "$(dirname "$0")/ensure-networks.sh"

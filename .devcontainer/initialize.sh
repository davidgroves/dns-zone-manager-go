#!/usr/bin/env bash
# Runs on the host before VS Code starts the devcontainer stack.
# Removes any Compose containers that reference a network that no longer exists
# (left over from a previous `docker network prune` or Docker Desktop restart),
# which otherwise makes `docker compose up` fail to recreate the stack.
#
# The dev image is pulled from GHCR, not built locally, so there is nothing here
# to work around build-time issues (apt GPG / VM clock); those only applied when
# the image was built on this machine.
set -euo pipefail

bash "$(dirname "$0")/ensure-networks.sh"

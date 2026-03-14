#!/usr/bin/env bash
set -euo pipefail

# Runtime dependencies for dictate on Debian/Ubuntu.
# Run once: sudo ./scripts/install-runtime.sh

if [ "$(id -u)" -ne 0 ]; then
    echo "error: run with sudo" >&2
    exit 1
fi

DEPS=(libsdl2-2.0-0 wtype)

# Vulkan runtime — optional, for GPU inference
if ! dpkg -s libvulkan1 &>/dev/null; then
    DEPS+=(libvulkan1)
fi

echo "installing: ${DEPS[*]}"
apt-get update -qq
apt-get install -y --no-install-recommends "${DEPS[@]}"
echo "done"

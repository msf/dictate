#!/usr/bin/env bash
set -euo pipefail

if ! command -v wev >/dev/null 2>&1; then
    echo "error: wev is not installed" >&2
    exit 1
fi

echo "Press the hardware key once. Ctrl+C to exit." >&2

wev -f wl_keyboard | awk '
    /key: .*state: 1 \(pressed\)/ {
        key = $0
        next
    }
    key != "" && /sym:/ {
        print key
        print $0
        print ""
        key = ""
    }
'

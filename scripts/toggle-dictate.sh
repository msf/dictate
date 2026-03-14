#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="${XDG_RUNTIME_DIR:-/tmp}/dictate"
PIDFILE="$STATE_DIR/dictate.pid"
LOGFILE="$STATE_DIR/dictate.log"
DICTATE_BIN="${DICTATE_BIN:-$ROOT_DIR/bin/dictate}"

mkdir -p "$STATE_DIR"

is_running() {
    [[ -f "$PIDFILE" ]] || return 1
    local pid
    pid="$(<"$PIDFILE")"
    [[ -n "$pid" ]] || return 1
    kill -0 "$pid" 2>/dev/null
}

cleanup_stale_pid() {
    if [[ -f "$PIDFILE" ]] && ! is_running; then
        rm -f "$PIDFILE"
    fi
}

start_dictate() {
    cleanup_stale_pid
    if is_running; then
        echo "dictate already running (pid $(<"$PIDFILE"))"
        exit 0
    fi

    nohup "$DICTATE_BIN" --output type "$@" >>"$LOGFILE" 2>&1 &
    local pid=$!
    echo "$pid" >"$PIDFILE"
    echo "started dictate (pid $pid)"
}

stop_dictate() {
    cleanup_stale_pid
    if ! is_running; then
        echo "dictate not running"
        exit 0
    fi

    local pid
    pid="$(<"$PIDFILE")"
    kill -TERM "$pid"
    rm -f "$PIDFILE"
    echo "stopped dictate (pid $pid)"
}

status_dictate() {
    cleanup_stale_pid
    if is_running; then
        echo "running pid $(<"$PIDFILE")"
    else
        echo "stopped"
    fi
}

cmd="${1:-toggle}"
case "$cmd" in
    start)
        shift
        start_dictate "$@"
        ;;
    stop)
        stop_dictate
        ;;
    status)
        status_dictate
        ;;
    toggle)
        shift || true
        cleanup_stale_pid
        if is_running; then
            stop_dictate
        else
            start_dictate "$@"
        fi
        ;;
    *)
        echo "usage: $0 [toggle|start|stop|status] [dictate args...]" >&2
        exit 2
        ;;
esac

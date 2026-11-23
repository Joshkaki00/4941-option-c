#!/usr/bin/env bash
# Stop the Ask the Handbook server in one step.
#
# 1. Stops the process recorded in logs/server.pid (written by /serve).
# 2. Stops any other handbook server still holding port 8080 (for example one
#    started with `make run`). Any other process on the port is reported and left alone.
# 3. Confirms the port is free and reports whether the last shutdown was clean.
set -euo pipefail

cd "$(dirname "$0")/.."
PORT=8080
PID_FILE=logs/server.pid
LOG_FILE=logs/server.log

# stop_pid sends SIGTERM and waits up to 20s. It never sends SIGKILL.
stop_pid() {
	local pid=$1
	kill -0 "$pid" 2>/dev/null || return 0
	kill "$pid"
	for _ in $(seq 1 20); do
		kill -0 "$pid" 2>/dev/null || return 0
		sleep 1
	done
	echo "pid $pid still alive after 20s; not escalating" >&2
	return 1
}

if [ -f "$PID_FILE" ]; then
	pid=$(cat "$PID_FILE")
	if kill -0 "$pid" 2>/dev/null; then
		stop_pid "$pid" && echo "stopped pid $pid (from $PID_FILE)"
	fi
	rm -f "$PID_FILE"
fi

for pid in $(lsof -tiTCP:$PORT -sTCP:LISTEN 2>/dev/null || true); do
	cmd=$(ps -o command= -p "$pid" || true)
	case "$cmd" in
	*/server*|*cmd/server*)
		stop_pid "$pid" && echo "stopped pid $pid ($cmd)"
		;;
	*)
		echo "port $PORT is held by a process that is not a handbook server: pid $pid: $cmd" >&2
		exit 1
		;;
	esac
done

if lsof -nP -iTCP:$PORT -sTCP:LISTEN >/dev/null 2>&1; then
	echo "port $PORT is still in use" >&2
	exit 1
fi
echo "port $PORT free"

if [ -f "$LOG_FILE" ]; then
	if grep -q 'shutting down' "$LOG_FILE" && ! grep -qi 'panic' "$LOG_FILE"; then
		echo "last shutdown in $LOG_FILE: clean"
	else
		echo "last shutdown in $LOG_FILE: check the log (no 'shutting down' line, or a panic)" >&2
	fi
fi

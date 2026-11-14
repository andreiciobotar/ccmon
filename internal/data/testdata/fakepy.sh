#!/bin/sh
# Stand-in for `python3 -m isshoni <cmd> [id] --json ...` and for `python3 <root>/factory/bin/factory status --json`.
# Logs each call to $STUBDIR/calls; a file named fail or hang in $STUBDIR makes every call fail or never return.
# $STUBDIR/sessions.json and $STUBDIR/status.json, when present, are what the sessions and factory calls print.
if [ "$2" = status ]; then
	echo "factory $PWD" >> "$STUBDIR/calls"
	[ -e "$STUBDIR/fail" ] && { echo "factory: not inside a git repository" >&2; exit 2; }
	[ -e "$STUBDIR/hang" ] && { sleep 30 & echo $! > "$STUBDIR/child"; wait; }
	[ -e "$STUBDIR/status.json" ] && exec cat "$STUBDIR/status.json"
	echo '{"rows":[],"cap":5,"open":0,"in_motion":0,"blocked":0,"in_review":0,"merged":0,"settled":0}'
	exit
fi
echo "$3 $4" >> "$STUBDIR/calls"
[ -e "$STUBDIR/fail" ] && { echo "boom" >&2; exit 1; }
[ -e "$STUBDIR/hang" ] && exec sleep 30
case "$3" in
session) printf '{"id":"%s","project":"p","tokens":{"total":10},"dispatches":[]}' "$4" ;;
kaizen) [ -e "$STUBDIR/kaizen.json" ] && exec cat "$STUBDIR/kaizen.json"; echo "{}" ;;
agents) echo '{"agents":[],"by_model":{},"sessions":0}' ;;
*) [ -e "$STUBDIR/sessions.json" ] && exec cat "$STUBDIR/sessions.json"; echo '{"sessions":[],"malformed":0}' ;;
esac

#!/bin/sh
# Stand-in for `python3 -m ccmon <cmd> [id] --json ...`. Logs each call to $STUBDIR/calls;
# a file named fail or hang in $STUBDIR makes every call fail or never return.
echo "$3 $4" >> "$STUBDIR/calls"
[ -e "$STUBDIR/fail" ] && { echo "boom" >&2; exit 1; }
[ -e "$STUBDIR/hang" ] && exec sleep 30
case "$3" in
session) printf '{"id":"%s","project":"p","tokens":{"total":10},"dispatches":[]}' "$4" ;;
agents) echo '{"agents":[],"by_model":{},"sessions":0}' ;;
*) echo '{"sessions":[],"malformed":0}' ;;
esac

#!/bin/sh
# Walk the watch's whole life on this machine with the real scheduler and no Claude login:
# stub `claude` and `gh`, a throwaway repository, then hook start, a forced tick, the tick that
# finds no dispatcher and removes the job, manual install/remove, and the SessionEnd stop.
# It installs a real per-user job (removed again on exit) and writes to ~/.claude/factory, so it
# runs only in CI or with WALK=1. WALK_CRON=1 hides systemd so that the cron backend is chosen.
set -eu

[ "${CI:-}" = true ] || [ "${WALK:-}" = 1 ] || { echo "walk: refusing outside CI; set WALK=1 to run it here" >&2; exit 2; }

here=$(cd "$(dirname "$0")/.." && pwd)
factory="$here/factory/bin/factory"
work=$(mktemp -d)
work=$(cd "$work" && pwd -P)
repo="$work/walk-repo"
stub="$work/stub"
log="$HOME/.claude/factory/watch.log"
unset CLAUDE_PROJECT_DIR CLAUDE_CODE_SESSION_ID
[ "${WALK_CRON:-}" = 1 ] && unset XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS

say() { printf '\n== %s\n' "$*"; }
die() { echo "walk: FAILED: $*" >&2; exit 1; }
status() { (cd "$repo" && "$factory" schedule status); }
cleanup() { (cd "$repo" 2>/dev/null && "$factory" schedule remove >/dev/null 2>&1) || true; rm -rf "$work"; }
trap cleanup EXIT

say "platform facts"
uname -sr
for tool in launchctl systemctl loginctl crontab notify-send osascript xdg-open google-chrome chromium; do
    printf '%-14s %s\n' "$tool" "$(command -v "$tool" || echo absent)"
done
if command -v systemctl >/dev/null; then
    systemctl --user show-environment >/dev/null 2>&1 && echo "systemd user session: yes" || echo "systemd user session: no"
fi

mkdir -p "$stub" "$repo"
cat > "$stub/claude" <<EOF
#!/bin/sh
[ "\$1" = agents ] && exec cat "$stub/sessions.json"
exit 1
EOF
printf '#!/bin/sh\necho "[]"\n' > "$stub/gh"
chmod +x "$stub/claude" "$stub/gh"
PATH="$stub:$PATH"
export PATH
git -C "$repo" init -q
git -C "$repo" -c user.name=walk -c user.email=walk@example.invalid commit -q --allow-empty -m walk

alive() {
    cat > "$stub/sessions.json" <<EOF
[{"pid": $$, "cwd": "$repo", "kind": "interactive", "startedAt": 0, "sessionId": "walk-1",
  "name": "dispatcher-walk-repo", "status": "idle"}]
EOF
}
gone() { echo "[]" > "$stub/sessions.json"; }
hook() { printf '{"hook_event_name": "%s", "session_id": "walk-1", "cwd": "%s", "prompt": "hi"}' "$1" "$repo" | "$factory" hook; }
expect() { # expect <words in status> <what>
    out=$(status)
    echo "$out"
    case "$out" in *"$1"*) ;; *) die "$2: status does not say '$1'" ;; esac
}
native() { # the scheduler's own view of the job
    case "$backend" in
        launchd) launchctl print "gui/$(id -u)/$label" | grep -E "state|run interval|program" ;;
        systemd) systemctl --user list-timers --all "$label.timer" --no-pager ;;
        cron) crontab -l | grep -F "$label" ;;
    esac
}
tick() { # force one scheduled run and wait for it
    case "$backend" in
        launchd) launchctl kickstart "gui/$(id -u)/$label" ;;
        systemd) systemctl --user start "$label.service" ;;
        cron) sh -c "$(crontab -l | grep -F "# $label" | cut -d' ' -f6-)" ;;
    esac
}
wait_for() { # wait_for <seconds> <command...>
    n=$1; shift
    while [ "$n" -gt 0 ]; do "$@" && return 0; sleep 1; n=$((n - 1)); done
    return 1
}
logged() { grep -q "$1" "$log" 2>/dev/null; }
absent() { case "$(status)" in *absent*) return 0 ;; *) return 1 ;; esac; }

say "hook: a prompt in the dispatcher session installs the job"
alive
expect absent "before the hook"
hook UserPromptSubmit
expect "present, loaded" "after the hook"
label=$(status | cut -d: -f1)
backend=$(status | sed -E 's/^[^:]*: ([a-z]+) job.*/\1/')
echo "label=$label backend=$backend"
native

say "a forced tick runs watch in the repository"
: > "$log"
tick
wait_for 30 logged "watch walk-repo" || { cat "$log"; die "the tick did not write to $log"; }
cat "$log"
expect "present, loaded" "after a tick with a live dispatcher"

say "no dispatcher session: the next tick removes the job"
gone
tick
wait_for 30 absent || { cat "$log"; die "the job did not remove itself"; }
cat "$log"
logged "schedule removed" || die "the log does not say the schedule was removed"
status

say "manual control"
alive
(cd "$repo" && "$factory" schedule install)
expect "present, loaded" "after schedule install"
(cd "$repo" && "$factory" schedule remove)
expect absent "after schedule remove"

say "SessionEnd of the last dispatcher removes the job"
hook SessionStart
expect "present, loaded" "after SessionStart"
hook SessionEnd
expect absent "after SessionEnd"

say "walk passed ($backend)"

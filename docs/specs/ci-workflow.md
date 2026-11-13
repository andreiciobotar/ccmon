# CI workflow

## Outcome

GitHub Actions runs every test suite on each pull request (any base branch) and on each push to `main`, and the run fails when any suite fails.

## Design

One file, `.github/workflows/test.yml`, one job on `ubuntu-latest`:

1. `actions/checkout@v7`
2. `actions/setup-go@v7` with `go-version-file: go.mod` (the Go version has one source: `go.mod`)
3. `actions/setup-python@v7` with Python 3.11
4. `make test`
5. `go test -race ./...`

No application code, test or Makefile changes.

## Decisions and defaults

- **`make test` is the list of suites.** The workflow calls it instead of repeating its commands, so a suite added to `make test` runs in CI with no workflow edit. The price: the Go tests run twice (plain, then with the race detector), a few seconds. `make test` itself stays without `-race`, so local runs do not need a C compiler.
- **Python 3.11, not the newest.** 3.11 is the oldest version the code supports; testing the floor catches use of newer features. Newer versions are not tested in CI (leads run them locally; local is 3.12).
- **Linux only.** The tests have no macOS dependency (see facts). macOS, where the tool is developed and used, is not tested in CI.
- **Triggers:** `pull_request` with no branch filter (so stacked pull requests run), `push` to `main` only (no double run for a branch that has a pull request).
- **`permissions: contents: read`:** the job only reads the repository.
- **Concurrency:** a new push to the same ref cancels the run in progress for that ref. This also applies to `main`: two quick pushes leave the first one's run cancelled.
- **`timeout-minutes: 10`:** a hung test fails the run in 10 minutes instead of the default 6 hours. A local run takes under 15 seconds.
- **Action versions pinned by major tag** (`@v7`), not by commit SHA: they are GitHub's own actions, and a tag needs no upkeep. The Go module cache that setup-go keeps by default (keyed on `go.sum`) stays on.

## Non-goals

Lint beyond `go vet`, coverage, a build matrix, release builds, the `make screenshot` path (needs Chrome), branch protection (a repository setting: making the check required is the owner's call).

## Facts this depends on

Checked 2026-11-13.

- Latest releases: checkout v7.0.1, setup-go v7.0.0, setup-python v7.1.0 (GitHub releases API). setup-go has `go-version-file` and caches by default; setup-python has `python-version` (each action's `action.yml`).
- `go.mod` says `go 1.27`; the setup-go version manifest has 1.27.4.
- Repository is public, Actions enabled, all actions allowed, default branch `main`; no CI config exists yet.
- Locally (macOS, Go 1.27.3, Python 3.12.4): `make test` passes (57 + 49 Python tests, `go vet`, 3 Go packages); `go test -race ./...` passes; Go tests pass with `LANG=C LC_ALL=C`.
- Full count of what the tests need from the machine (explorer, all `*.go`, `*.py`, Makefile and shebang files):
  - Programs really executed: `python3` on `PATH` (Go fixture tests, fake `claude`/`gh` shebangs), `git` (factory end-to-end tests; `git init -b` needs git 2.28+), `/bin/sh` and `sleep` (`internal/data/testdata/fakepy.sh`), `make`. `claude`, `gh` and `tmux` are always faked. The race detector needs a C compiler. All are on the `ubuntu-latest` image.
  - Platform: no `darwin`, `runtime.GOOS`, `sys.platform` or macOS path in code or tests; the only one is `CHROME` in the Makefile, used by `make screenshot` alone.
  - Home, identity, network, TTY, timezone: tests point at fixture directories or a temporary `HOME`; git identity comes from `GIT_*` variables the test sets; no network; no TTY; the Go TUI tests set `time.Local = time.UTC` and Python uses UTC explicitly.
  - Skips: six `t.Skip` sites in the Go tests, none in Python. Four skip when `python3` or the stub runner is unusable, two when directory permissions are not enforced (root). On the runner (Python installed, non-root user) none should fire.

## Known limits

- A skipped Go test does not fail the run, and `go test` without `-v` does not print skips. If `python3` ever went missing on the runner, the fixture and golden tests would skip and the run would stay green.
- Golden TUI output depends on the locale through go-runewidth (East Asian width). It passes under `C` locally and must pass on the runner (`C.UTF-8`); CJK locales are not tested.

## Done when

- The workflow has run green on this task's pull request.
- A deliberately failing test makes the run fail (checked once on a throwaway commit or by reading the step's exit status; not left in the branch).
- The pull request body states how long a run takes.

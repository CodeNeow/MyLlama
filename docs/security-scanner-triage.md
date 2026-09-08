# Security Scanner Triage — Mimosa Pre-Commit Findings (issue #26)

Disposition of the 8 pre-existing findings reported by the Mimosa L3 pre-commit
scanner (tracked in issue #26). These were flagged as pattern matches on a
full-project scan; each was reviewed manually. None was assessed to be an
actual vulnerability: every exec site launches a fixed, locally resolved
target with arguments derived from the local configuration or compile-time
constants — there is no untrusted input in any command line.

| # | Finding (as reported) | Current location | Disposition |
| --- | --- | --- | --- |
| 1 | Command-injection pattern — `llama-server --version` probe | `core/llamacpp.go:217` | reviewed: local trusted input (launch target from the local llama.cpp install lookup, `--version` is a constant) |
| 2 | Command-injection pattern — `runCmd` generic exec helper | `core/fsutil.go:36` | reviewed: local trusted input (callers pass fixed binary names and config-derived args) |
| 3 | Command-injection pattern — llama-server launch | `core/bridge.go:232` | reviewed: local trusted input (binary resolved locally, args from local config; the API key is delivered via the `LLAMA_API_KEY` env var, see #8) |
| 4 | Command-injection pattern — headless relaunch bootstrap | `core/headless.go:65` | reviewed: local trusted input (`os.Executable()` self-relaunch with fixed mode flags) |
| 5 | Command-injection pattern — non-Windows installer launch | `core/installer_launch_other.go:10` | reviewed: local trusted input (path of the installer the app itself downloaded) |
| 6 | Command-injection pattern — Go helper-process test | `core/server_test.go:524` | reviewed: local trusted input (standard Go test helper-process idiom, `os.Args[0]` + `-test.run` flag) |
| 7 | Command-injection pattern — Go helper-process test | `core/singleinstance_windows_test.go:87` | reviewed: local trusted input (same Go test idiom as #6) |
| 8 | Hardcoded credential — fake token fixture in `TestBuildServerCommandAPIKey` | `core/server_test.go` | resolved: API key now delivered via the `LLAMA_API_KEY` env var; the argv fixture was removed (tests assert argv is key-free and the env delivery separately) |

## Process note

Findings of this kind are triaged per release: each scanner report is reviewed
manually against the actual call sites, and the disposition is recorded here so
the same decision does not have to be re-made by hand on every commit. Scanner
exemptions are environment-local (they live in the scanning machine's Mimosa
configuration) and are intentionally not committed to this repository.

# Contributor Guide

Thank you for contributing to MyLlama! This guide covers environment setup, commit conventions, quality gates, and the Issue / PR workflow. For the complete development guidelines, see [AGENTS.md](./AGENTS.md).

## 1. Environment Setup

| Dependency | Version | Notes |
| --- | --- | --- |
| [Go](https://go.dev/dl/) | 1.25+ | Backend (declared in `go.mod`) |
| [Node.js](https://nodejs.org/) | 18+ | Frontend build (CI uses 24) |
| [Wails CLI](https://wails.io/docs/gettingstarted/installation) | v3 | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.16` (keep the pinned version in sync with `go.mod`) |
| [golangci-lint](https://golangci-lint.run/) | v2 | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.9.0` |
| Android toolchain (optional) | JDK 17 + NDK | Only needed for `wails3 task android:*`; install an NDK via `sdkmanager "ndk;26.3.11579264" "platforms;android-35"` |

Local development:

```bash
wails3 task dev    # Go backend + Vite frontend (:5173) hot-reload
```

> The frontend calls the backend through the Wails v3 generated bindings (`frontend/bindings/`, re-exported by `frontend/src/wails.ts`). Running `npm run dev` standalone (without the Wails runtime) makes backend calls fail at fetch time — this is expected behavior. Use `npm run dev:mock` to debug the UI in a plain browser against the in-repo mock runtime.

## 2. Branches and Commits

### 2.1 Branches

- Active integration happens on `dev` — commit there by default. `main` is the release channel: it advances only at release time (version-bump + CHANGELOG commit, then the annotated tag), so ordinary work never targets it directly. Only create a feature branch when the change needs isolated review (e.g., opening a PR).

### 2.2 Commit Messages

Use the format `type(scope): English subject`, with the subject on a single line. Existing types: `feat` / `fix` / `docs` / `chore` / `refactor` / `test` / `perf` / `security`. Scopes such as `backend` / `frontend` / `build` / etc., used only when meaningful.

```text
feat(backend): support custom llama.cpp directory per model
fix(backend): fix download task not resuming after pause
docs: update API page usage instructions
```

The detailed body must include the following structure:

```text
Summary:
- <primary changes, grouped by domain: Backend / Frontend / Tests / Docs, etc.>

Verification:
- <actual verification commands run and pass results>

Remaining gaps:
- <explicitly state unfinished follow-up work; write None if there are none>
```

- Cross-frontend-backend changes (e.g., adding binding methods) must land in a single commit to avoid intermediate states.
- Before committing, `git status --short` must contain only intentionally modified files for the current task; `git diff --check` must produce no errors.

### 2.3 Multi-tool Collaboration Roles

When using multi-agent / multi-session collaboration, roles are declared by the user at the start of the task: **Reviewer** (formulates plan, reviews diff, commits after acceptance), **Implementation Agent** (implements and self-tests per the task package, stops at an unstaged working tree), **Issues Finder** (interactive verification then creates remote issues per conventions). See [AGENTS.md](./AGENTS.md) "Multi-role Collaboration and Workflows" for detailed responsibilities.

## 3. Quality Gates (Required Before Commit)

| Scope | Command | Requirement |
| --- | --- | --- |
| Backend | `go build ./...` | Compilation passes |
| Backend | `go test ./...` | Unit tests pass |
| Backend | `gofmt -l .` | No output |
| Backend | `golangci-lint run` | Zero diagnostics (govet / ineffassign / unused) |
| Frontend | `cd frontend && npm run build` | vue-tsc type-check + vite build zero errors |
| Frontend | `cd frontend && npm test` | vitest unit tests pass |

Combined quality gate: use `make check` on POSIX; use `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\check.ps1` on Windows. Choose local vs. full verification per change scope; tiering rules are in [AGENTS.md](./AGENTS.md) "Pre-commit Test Tiers".

> CI (`.github/workflows/ci.yml`) runs on every push / PR: `frontend` (type-check + build + vitest, uploads the `frontend-dist` artifact for the go:embed builds), `backend` (ubuntu, `gtk3`-tagged go build + tests + gofmt + golangci-lint, plus service-chain E2E against a real llama-server and a tiny GGUF in both router mode and the Android direct mode), `build-windows` (`wails3 task build`, NSIS installer, windows-branch unit tests + router-mode E2E), `build-linux` (Ubuntu 22.04 / 24.04 `.deb` packages on the GTK3 stack), `build-macos` (universal `.app` zip, darwin-branch unit tests + router-mode E2E), `build-android` (arm64 release APK from `build/android`, plus an x86_64 smoke APK) and `smoke-android` (x86_64 APK booted on an API-30 emulator with logcat assertions and screenshots). A `v*` tag push additionally triggers `release`, which publishes the 5-artifact GitHub Release with notes extracted from CHANGELOG.md. Local passes do not replace CI's final determination.

## 4. Issue Workflow

### 4.1 When to Open an Issue

Non-trivial defects and planned work should be filed as issues to make progress visible; for questions and design discussions, prefer Discussions.

### 4.2 How to Open

- Daily bugs use the "Bug Report" template (`.github/ISSUE_TEMPLATE/bug-report.yml`).
- Structured findings from audits or code reviews use the "Audit Finding" template (`audit-finding.yml`) and must include `file:line` evidence and acceptance criteria.
- Batch findings (e.g., a full audit): create a Tracker overview issue first, then separate child issues for P0/P1; P2/P3 may be merged into a summary.

### 4.3 Priority and Labels

Titles add a severity prefix: `[P0]` / `[P1]` / `[P2]` / `[P3]` / `[Bug]` / `[Tracker]`. Priority reflects **impact** rather than effort:

- `P0`: blocks core functionality or is a security vulnerability
- `P1`: materially harms experience or security
- `P2`: consistency / polish gap
- `P3`: nitpick-level minor issue

The label set (including P0–P3 and area labels) is created in one shot via `scripts/create-labels.ps1`:

```powershell
gh auth login   # required on first use
.\scripts\create-labels.ps1
```

### 4.4 Sensitive Security Vulnerabilities

Issues involving credentials, injection, or privilege escalation **must not** be filed publicly. Report them privately via GitHub Security Advisory.

### 4.5 Issue Content Redaction

Issue bodies must never contain tokens, secrets, or machine-specific absolute paths (such as paths from `myllama-config.json`). Redact before submitting.

## 5. Pull Request Workflow

1. Cut a feature branch from `dev`; commit conventions are in Section 2.
2. Ensure all local quality gates (Section 3) pass.
3. Open a PR: use `Fixes #N` to associate the corresponding issue (if any), and attach verification commands and results in the description.
4. After CI is fully green, a maintainer reviews and merges.

## 6. Code of Conduct

- When fixing bugs, limit changes to the fault site and its related files; do not mix in unrelated refactoring.
- When adding or modifying backend binding methods, always update `frontend/src/wails.ts` and callers in sync.
- New behavior must include focused tests (Go `*_test.go` / vitest); do not pass quality gates by deleting failing tests or skipping verification.
- Do not commit generated artifacts or local config: `node_modules/`, `frontend/dist/`, `build/`, model files under `LLM-Models/`, `myllama-config.json`, `*.log`.
- User-visible copy is bilingual via the i18n dictionary (`lib/i18n.ts`); never hardcode UI strings.

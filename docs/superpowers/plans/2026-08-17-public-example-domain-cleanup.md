# Public Example Domain Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace private-domain examples with `example.com` and rebuild the public GitHub repository without the old reachable history.

**Architecture:** Perform a mechanical tracked-file replacement, verify behavior, then create a new orphan root commit. Delete and recreate the same public repository only after the sanitized root passes all gates, and independently verify the resulting CI release.

**Tech Stack:** Go, Git, GitHub CLI, GitHub Actions, go-semantic-release

---

### Task 1: Sanitize tracked examples

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-08-17-go-root-python-removal-design.md`
- Modify: `internal/cloudflare/client_test.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/dnscheck/check_test.go`
- Modify: `internal/dnspod/decision_test.go`
- Modify: `internal/domain/domain_test.go`
- Modify: `internal/workflow/workflow_test.go`

- [ ] **Step 1: Confirm the privacy scan fails**

Run a case-insensitive `git grep` for the private domain while excluding `.env`.
Expected: 48 matches across the eight tracked files above.

- [ ] **Step 2: Replace the domain suffix**

Replace only the private domain suffix with `example.com`, preserving each
subdomain and test scenario.

- [ ] **Step 3: Confirm the privacy scan passes**

Run the same `git grep` command.
Expected: no output and exit status 1 from `git grep`.

- [ ] **Step 4: Verify Go behavior**

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
make build
```

Expected: every command exits successfully.

### Task 2: Replace local Git history

**Files:**
- Preserve untracked: `.env`
- Commit: all tracked project files

- [ ] **Step 1: Verify `.env` protections**

Confirm `.env` exists, is ignored, and is absent from `git ls-files`.

- [ ] **Step 2: Create a new orphan root**

Create an orphan branch from the sanitized working tree, stage tracked files
explicitly, commit with `feat: initial Go implementation`, and rename it to
`main`.

- [ ] **Step 3: Remove old local references**

Remove obsolete local tags and remote-tracking references so the new repository
cannot receive old objects accidentally.

- [ ] **Step 4: Rescan all reachable commits**

Run the private-domain scan against every commit returned by `git rev-list
--all`.
Expected: zero matches.

### Task 3: Recreate the public GitHub repository

**Files:**
- Deploy: sanitized `main`

- [ ] **Step 1: Delete the old repository**

Delete exactly `oustn/cloudflare-dns-dnspod` after confirming the sanitized
local root commit exists. This intentionally removes the old releases and the
three Renovate pull requests.

- [ ] **Step 2: Recreate and push**

Create the same repository name with public visibility, set `origin`, and push
the sanitized `main` branch.

- [ ] **Step 3: Verify remote history**

Confirm public visibility, `main` as the default branch, one reachable commit,
and zero private-domain matches in the GitHub-hosted tree.

### Task 4: Verify replacement CI and release

**Files:**
- Verify: `.github/workflows/release.yml`

- [ ] **Step 1: Watch the replacement workflow**

Wait for the `CI and Release` workflow triggered by the new root commit.
Expected: `verify` and `release` jobs both conclude `success`.

- [ ] **Step 2: Validate release assets**

Download all release assets, run `shasum -a 256 -c SHA256SUMS`, and inspect
formats with `file`.
Expected: static Linux amd64/arm64 ELF files and a macOS arm64 Mach-O file.

- [ ] **Step 3: Scan the public result**

Scan the GitHub tree, release metadata, and extracted binary strings for the
private domain.
Expected: zero matches. Confirm `.env` still exists locally, is ignored, and is
not tracked.


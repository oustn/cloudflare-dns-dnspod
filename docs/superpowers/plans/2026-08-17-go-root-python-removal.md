# Go Root Migration And Python Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Go CLI the repository-root default, migrate the existing configuration to root `.env`, and remove all Python and quarantine assets.

**Architecture:** Promote the already-tested Go module without changing package paths or behavior. Perform all collision and preservation checks before destructive operations, move `.env` without reading it, then remove only the approved Python, output, cache, and quarantine paths.

**Tech Stack:** Go 1.25, Make, POSIX filesystem operations

---

### Task 1: Validate Migration Preconditions

**Files:**
- Read metadata only: `outputs/cloudflare-dnspod-cli/.env`
- Must not exist: `.env`, `cmd/`, `internal/`, `go.mod`, `go.sum`, `Makefile`, `dist/`

- [ ] **Step 1: Verify the source configuration exists and root target is absent**

Run metadata-only existence checks without opening either file:

```bash
test -e outputs/cloudflare-dnspod-cli/.env
test ! -e .env
```

Expected: both commands exit `0`.

- [ ] **Step 2: Verify Go promotion targets do not collide**

```bash
for path in cmd internal go.mod go.sum Makefile dist; do test ! -e "$path"; done
```

Expected: exit `0`.

### Task 2: Promote Go And Configuration

**Files:**
- Move: `go/cmd/` to `cmd/`
- Move: `go/internal/` to `internal/`
- Move: `go/go.mod` to `go.mod`
- Move: `go/go.sum` to `go.sum`
- Move: `go/Makefile` to `Makefile`
- Replace: `README.md` with `go/README.md`
- Modify: `.gitignore` to retain `.env` protection and add `/dist/`
- Move: `go/dist/` to `dist/`
- Move: `outputs/cloudflare-dnspod-cli/.env` to `.env`

- [ ] **Step 1: Remove only the old root README before promotion**

```bash
rm README.md
```

Expected: exit `0`; no source or configuration file is removed.

- [ ] **Step 2: Move Go project paths to root**

```bash
mv go/cmd cmd
mv go/internal internal
mv go/go.mod go.mod
mv go/go.sum go.sum
mv go/Makefile Makefile
mv go/README.md README.md
mv go/dist dist
```

Expected: every move exits `0`; root is now a valid Go module.

- [ ] **Step 3: Replace Python ignore rules while retaining secret and build protection**

Use `apply_patch` to make root `.gitignore` exactly:

```gitignore
.env
/dist/
```

Expected: root `.env` and release artifacts remain excluded from version control.

- [ ] **Step 4: Move configuration without reading or rewriting it**

```bash
mv outputs/cloudflare-dnspod-cli/.env .env
```

Expected: exit `0`; the file exists only at root.

### Task 3: Remove Python And Quarantine Assets

**Files:**
- Delete root Python files and environments listed by the approved design.
- Delete remaining `outputs/cloudflare-dnspod-cli/` content.
- Delete Python-only historical docs.
- Delete `work/quarantine/` and empty parent directories.

- [ ] **Step 1: Delete explicit root Python files and directories**

```bash
rm -f cf_dnspod.py cloudflare_api.py dns_resolver.py dnspod_api.py models.py pyproject.toml uv.lock .python-version .DS_Store
rm -rf tests __pycache__ .pytest_cache .venv
```

Expected: listed Python assets no longer exist.

- [ ] **Step 2: Delete the old Python output after `.env` has moved**

```bash
rm -rf outputs/cloudflare-dnspod-cli
rmdir outputs 2>/dev/null || true
```

Expected: the old output directory is absent; root `.env` remains.

- [ ] **Step 3: Delete quarantine and empty work directory**

```bash
rm -rf work/quarantine
rmdir work 2>/dev/null || true
```

Expected: `work/quarantine` is absent.

- [ ] **Step 4: Remove Python-only historical plans/specs and empty nested Go directory**

```bash
rm -f docs/superpowers/plans/2026-08-14-cloudflare-dnspod-custom-hostname.md
rm -f docs/superpowers/plans/2026-08-17-cloudflare-dual-zone.md
rm -f docs/superpowers/plans/2026-08-17-dnspod-child-zone-validation.md
rm -f docs/superpowers/specs/2026-08-14-cloudflare-dnspod-custom-hostname-design.md
rm -rf go
```

Expected: only Go and migration design/plan documents remain.

### Task 4: Verify Root Go Project And Removal

**Files:**
- Verify: `.env`, `cmd/`, `internal/`, `go.mod`, `go.sum`, `Makefile`, `README.md`, `.gitignore`, `dist/`

- [ ] **Step 1: Verify filesystem postconditions without reading `.env`**

```bash
test -e .env
test ! -e outputs/cloudflare-dnspod-cli
test ! -e work/quarantine
find . -type f \( -name '*.py' -o -name 'pyproject.toml' -o -name 'uv.lock' -o -name '.python-version' \) -print
find . -type d \( -name '.venv' -o -name '__pycache__' -o -name '.pytest_cache' \) -print
```

Expected: existence checks exit `0`; both `find` commands print nothing.

- [ ] **Step 2: Verify documentation has no former Python commands**

```bash
rg -n 'uv run|cf_dnspod\.py|pytest|Python implementation' README.md docs cmd internal || true
```

Expected: no obsolete runtime instruction remains.

- [ ] **Step 3: Run root-level Go verification**

```bash
go test ./...
go test -race ./...
go vet ./...
make build
```

Expected: all commands exit `0`.

- [ ] **Step 4: Verify release artifacts and CLI smoke tests**

```bash
file dist/cf-dnspod-linux-amd64 dist/cf-dnspod-linux-arm64 dist/cf-dnspod-darwin-arm64
./dist/cf-dnspod-darwin-arm64 --help
./dist/cf-dnspod-darwin-arm64 set-edge --subdomain dns-test --ip 192.0.2.1
```

Expected: Linux artifacts are static stripped ELF, macOS is arm64 Mach-O, help exits `0`, and invalid non-public IP exits `2` before provider calls.

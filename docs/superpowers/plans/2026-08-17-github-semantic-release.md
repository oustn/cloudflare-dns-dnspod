# GitHub CI And Semantic Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Initialize the Go CLI as a Git repository, verify/build it in GitHub Actions, publish semantic releases with attached binaries, and push it to the public `oustn/cloudflare-dns-dnspod` repository.

**Architecture:** One workflow verifies every push and pull request, transfers a single verified build artifact to a main-only release job, and lets `go-semantic-release/action@v1` create the version/tag/release before uploading binaries. Local Git checks explicitly prove `.env` is ignored and never staged before the public remote is created.

**Tech Stack:** Git, GitHub CLI, GitHub Actions, Go 1.25, Make, go-semantic-release/action@v1

---

### Task 1: Create And Validate The Release Workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Confirm workflow does not exist**

Run:

```bash
test ! -e .github/workflows/release.yml
```

Expected: exit `0`.

- [ ] **Step 2: Create the workflow**

Create `.github/workflows/release.yml` with exactly these responsibilities:

```yaml
name: CI and Release

on:
  push:
    branches:
      - "**"
  pull_request:

permissions:
  contents: read

concurrency:
  group: release-${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - name: Check out repository
        uses: actions/checkout@v4
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Test
        run: go test ./...
      - name: Race test
        run: go test -race ./...
      - name: Vet
        run: go vet ./...
      - name: Build release binaries
        run: make build
      - name: Create checksums
        run: cd dist && sha256sum cf-dnspod-* > SHA256SUMS
      - name: Upload verified binaries
        uses: actions/upload-artifact@v4
        with:
          name: release-binaries
          path: |
            dist/cf-dnspod-*
            dist/SHA256SUMS
          if-no-files-found: error
          retention-days: 7

  release:
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    needs: verify
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - name: Check out full history
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Download verified binaries
        uses: actions/download-artifact@v4
        with:
          name: release-binaries
          path: dist
      - name: Create semantic release
        id: semrel
        uses: go-semantic-release/action@v1
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          allow-initial-development-versions: true
      - name: Upload release assets
        if: steps.semrel.outputs.version != ''
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          RELEASE_VERSION: ${{ steps.semrel.outputs.version }}
        run: gh release upload "v${RELEASE_VERSION}" dist/* --clobber
```

- [ ] **Step 3: Validate workflow syntax and policy**

Run:

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/release.yml
```

Expected: exit `0` with no findings.

### Task 2: Update Public Repository Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Remove the stale Python coexistence statement**

Change the opening paragraph to describe the Go CLI as the repository implementation, without mentioning a Python version.

- [ ] **Step 2: Add CI and release documentation**

Add a `CI 和 Release` section stating:

```markdown
## CI 和 Release

GitHub Actions 会在每次 push 和 pull request 时执行测试、race test、vet 及三平台构建。`main` 分支使用 Conventional Commits 和 `go-semantic-release` 自动发布语义版本。

每个 GitHub Release 包含 Linux amd64、Linux arm64、macOS arm64 三个二进制文件以及 `SHA256SUMS`。本地 `.env` 被 Git 忽略，不会进入仓库或构建产物。
```

- [ ] **Step 3: Verify documentation no longer describes Python coexistence**

Run:

```bash
rg -n 'Python|并存|go-semantic-release|SHA256SUMS|\.env' README.md
```

Expected: no Python/coexistence statement; release and `.env` protection text are present.

### Task 3: Run Local Verification Before Public Push

**Files:**
- Verify: all Go source, tests, workflow, and build outputs

- [ ] **Step 1: Run Go verification**

```bash
go test ./...
go test -race ./...
go vet ./...
make build
```

Expected: all commands exit `0`.

- [ ] **Step 2: Verify artifact formats**

```bash
file dist/cf-dnspod-linux-amd64 dist/cf-dnspod-linux-arm64 dist/cf-dnspod-darwin-arm64
```

Expected: Linux files are static stripped ELF for amd64/arm64; macOS is arm64 Mach-O.

### Task 4: Initialize Git And Create Initial Commit

**Files:**
- Create: `.git/`
- Stage: all non-ignored project files
- Never stage: `.env`, `dist/`

- [ ] **Step 1: Initialize main branch**

```bash
git init -b main
```

Expected: repository initialized with branch `main`.

- [ ] **Step 2: Prove secrets and binaries are ignored**

```bash
git check-ignore -v .env dist/cf-dnspod-darwin-arm64
```

Expected: `.gitignore` rules are printed for both paths.

- [ ] **Step 3: Stage and audit every path**

```bash
git add .
git diff --cached --name-only
git diff --cached --name-only | rg '(^|/)\.env$' && exit 1 || true
```

Expected: project files and workflow are staged; `.env` and `dist/` are absent.

- [ ] **Step 4: Confirm Git identity and commit**

```bash
git config user.name
git config user.email
git commit -m "feat: initial Go implementation"
```

Expected: identity commands return non-empty values and the root commit succeeds.

- [ ] **Step 5: Inspect the committed tree**

```bash
git status --short
git branch --show-current
git ls-tree -r --name-only HEAD | rg '(^|/)\.env$' && exit 1 || true
```

Expected: clean status, branch `main`, and no `.env` in the commit.

### Task 5: Create Public Repository And Verify Initial Release

**External resource:**
- Create: `https://github.com/oustn/cloudflare-dns-dnspod`

- [ ] **Step 1: Reconfirm target repository is absent**

```bash
gh repo view oustn/cloudflare-dns-dnspod --json nameWithOwner
```

Expected: repository-not-found error. If it exists, stop before creating or pushing.

- [ ] **Step 2: Create and push the public repository**

```bash
gh repo create oustn/cloudflare-dns-dnspod --public --source=. --remote=origin --push --description "Cloudflare for SaaS and DNSPod child-zone automation CLI"
```

Expected: public repository created, `origin` configured, and `main` pushed.

- [ ] **Step 3: Verify repository visibility and branch**

```bash
gh repo view oustn/cloudflare-dns-dnspod --json nameWithOwner,visibility,url,defaultBranchRef
```

Expected: visibility `PUBLIC` and default branch `main`.

- [ ] **Step 4: Find and follow the initial workflow**

```bash
gh run list --repo oustn/cloudflare-dns-dnspod --workflow release.yml --branch main --limit 1 --json databaseId,status,conclusion,url,headSha
release_run_id=$(gh run list --repo oustn/cloudflare-dns-dnspod --workflow release.yml --branch main --limit 1 --json databaseId --jq '.[0].databaseId')
test -n "$release_run_id"
gh run watch "$release_run_id" --repo oustn/cloudflare-dns-dnspod --exit-status
```

Expected: workflow completes successfully for the initial commit.

- [ ] **Step 5: Verify release and attached files**

```bash
gh release view v0.1.0 --repo oustn/cloudflare-dns-dnspod --json tagName,url,assets
```

Expected: tag `v0.1.0` and assets `cf-dnspod-linux-amd64`, `cf-dnspod-linux-arm64`, `cf-dnspod-darwin-arm64`, and `SHA256SUMS`.

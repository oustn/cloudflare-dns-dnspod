# Go Root Migration And Python Removal Design

## Goal

Make the tested Go CLI the repository's only and default implementation. A fresh checkout should be recognizable as a standard Go module from the repository root, with root-level build, test, documentation, and release commands.

## Go Root Layout

Move these Go-owned paths from `go/` to the repository root without changing package behavior:

```text
cmd/
internal/
go.mod
go.sum
Makefile
README.md
.gitignore (merged root rules for `.env` and `/dist/`)
dist/
```

After migration, default commands are:

```bash
go test ./...
go test -race ./...
go vet ./...
make build
./dist/cf-dnspod-darwin-arm64 --help
```

The nested `go/` directory is removed after all owned files have been promoted.

## Python Removal Scope

Delete all Python implementation and development artifacts from the repository root:

- `cf_dnspod.py`, `cloudflare_api.py`, `dns_resolver.py`, `dnspod_api.py`, and `models.py`;
- `tests/`, `__pycache__/`, `.pytest_cache/`, and `.venv/`;
- `pyproject.toml`, `uv.lock`, and `.python-version`;
- the old Python-oriented root README and ignore rules after their Go replacements are in place.

Delete the Python deliverable inside `outputs/cloudflare-dnspod-cli`, including its source, tests, virtual environment, caches, Python metadata, README, `.env.example`, and ignore file.

Delete Python-only historical design/plan documents. Retain the approved Go design, Go implementation plan, and this migration design.

## Configuration Preservation

Move the existing configuration file from the Python output directory to the repository root:

```text
outputs/cloudflare-dnspod-cli/.env -> .env
```

The migration performs a filesystem move only. It does not read, compare, rewrite, or expose the file contents. Before moving, verify that a root `.env` does not already exist so no configuration can be overwritten. After the move and Python cleanup, remove the empty `outputs/cloudflare-dnspod-cli/` directory and remove `outputs/` as well if it is empty.

Delete the entire `work/quarantine/` isolation area, including `work/quarantine/cloudflare-dnspod-cli.env`, as explicitly requested. Remove `work/` as well if it is empty afterward.

## Other Files

Remove repository-local `.DS_Store` files. Preserve unrelated user files unless they fall under an explicitly listed Python or quarantine path.

The live `dns-test.example.com` provider resources are outside the filesystem migration and remain unchanged.

## Verification

After migration:

1. Search the repository for Python source, Python project metadata, virtual environments, pytest caches, and references to `uv run` or the former Python CLI.
2. Confirm root `.env` exists and `outputs/cloudflare-dnspod-cli/.env` no longer exists, without inspecting file contents.
3. Confirm `outputs/cloudflare-dnspod-cli` and `work/quarantine` no longer exist.
4. Run root-level Go unit tests, race tests, vet, and all three release builds.
5. Confirm Linux amd64/arm64 binaries are static ELF and macOS arm64 is Mach-O.
6. Run CLI help and invalid-input smoke tests from the repository root.

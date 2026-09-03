# Release SOP — audit-sdk-go (PUBLIC repo)

`github.com/ab0t-com/audit-sdk-go` is a **public** Go module. Everything committed here is
world-readable, so a release is also a publication: no secret, no internal path, no private
service name may ever land in a tag. This SOP + `./release.sh` make cutting a release repeatable
and gate the public-safety checks so a mistake fails the release instead of shipping.

## Invariants (why each gate exists)
1. **No secrets.** `gitleaks` (config `.gitleaks.toml`) must find zero leaks. The runtime API key is
   supplied by the CONSUMER at construction — it is never in source.
2. **No internal references.** No internal service/codenames, no private monorepo paths, no internal
   ticket IDs, no internal skill names, no internal implementation-file paths. The lib documents the
   *public contract*, never internal internals. (`release.sh` enforces this with a scan.)
3. **Stdlib-only.** `go.mod` has no `require` block and there is no `go.sum` — the module pulls in
   zero transitive deps, so a consumer inherits nothing. A release must preserve this.
4. **Green.** `go build`, `go vet`, `gofmt -l` (empty), `go test -race` all pass.
5. **SemVer tag.** Releases are tagged `vMAJOR.MINOR.PATCH`; `version.go` `Version` matches the tag.

## Cut a release
```bash
cd shared/audit-sdk-go
./release.sh 0.1.1          # dry-run by default: runs every gate, shows what WOULD ship
./release.sh 0.1.1 --push   # after the dry-run is clean: bump version.go, commit, tag, push tag
```
`release.sh` (in order): asserts a clean tree → `gofmt -l` empty → `go build ./...` → `go vet ./...`
→ `go test -race ./...` → `gitleaks detect --no-git --config .gitleaks.toml` (0 leaks) → the
internal-reference scan (must be empty) → confirms `version.go` will match the tag. With `--push`:
sets `version.go`, commits, `git tag -a vX.Y.Z`, `git push origin main --follow-tags`, then verifies
the tag is fetchable via the Go proxy (`go list -m github.com/ab0t-com/audit-sdk-go@vX.Y.Z`).

## First-time repo setup (already done for v0.1.0)
```bash
git init && git branch -M main
git remote add origin https://github.com/ab0t-com/audit-sdk-go.git
# CI (.github/workflows/ci.yml) runs build/vet/gofmt/test + gitleaks on every push/PR.
```

## After a release — update consumers
Consumers pin the module from the proxy (no `replace`):
```
require github.com/ab0t-com/audit-sdk-go vX.Y.Z
```
Bump the version in the consumer's `go.mod` and `go mod tidy`. (The reference consumer already
pulls v0.1.0 from the proxy — no local replace.)

## Public-safety checklist (run before ANY push, enforced by release.sh + CI)
- [ ] `gitleaks detect --no-git --config .gitleaks.toml` → no leaks
- [ ] internal-ref scan empty (no internal service/codenames, private paths, or ticket IDs)
- [ ] `.gitignore` excludes creds, `.env*`, `tickets/`, `_internal/`, build output
- [ ] no `go.sum` / no `require` block (stdlib-only preserved)
- [ ] `version.go` == the tag · CHANGELOG note added below

## Changelog
### v0.1.0 — 2026-09-03
First public release. Sync `LogEvent` + fire-and-forget `Emit`/`EmitRaw` over a bounded background
queue (drop-on-saturation), draining `Close`, `Noop` null-object. Targets the ab0t Audit Service
ingest API (Product 1): `POST /logs/ingest`, `X-API-Key`, 202 == success, no-redirect-follow.

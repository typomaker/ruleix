# Ruleix v0.9.0 release

Ruleix v0.9.0 releases the unbounded per-`Local` result cache documented in
the changelog, index architecture, and performance history. `Index.Search`
remains the controlled-memory path for one-off queries.

## Release preparation

The release gate removed the 30 lint findings that predated the candidate
commits. The production changes preserve behavior while splitting streaming
Lossy compilation, detail refresh, build-pressure handling, and ordered ID
matching into focused helpers. Test-only findings were corrected by formatting
fixtures, using an actual negative floating-point zero, and removing redundant
declarations. The Local bitmap-pool test disables garbage collection while it
asserts pointer reuse because `sync.Pool` may otherwise discard an item during
any collection; the production best-effort reuse policy is unchanged. No lint
suppression or threshold change was introduced.

## Verification

On 2026-09-12, `go test ./...`, `go test -race ./...`, `golangci-lint run`,
and `git diff --check` passed with Go 1.26.0 on Darwin/arm64. Coverage collected
with `go test -coverprofile=/tmp/ruleix-v090.cover ./...` covered 126 of 140
changed production statements (90.0%). Every changed file remained within the
500-line limit. The release commit and annotated `v0.9.0` tag are published
together after these checks.

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

The initial preparation on 2026-09-12 verified that `go test ./...`,
`go test -race ./...`, `golangci-lint run`, and `git diff --check` passed with Go 1.26.0 on Darwin/arm64. Coverage collected
with `go test -coverprofile=/tmp/ruleix-v090.cover ./...` covered 126 of 140
changed production statements (90.0%). Every changed file remained within the
500-line limit. The final release validation is recorded below.

## Final release: 2026-11-18

The final v0.9.0 release also includes prepared narrow candidate execution,
reusable candidate masks and exact projections, direct `Between` endpoint
validation, and early empty checks prepared during Build. The implementation
uses portable Go without assembly and preserves the current public API.
Earlier v0.9.0 preparation removed the unused `Visit` API; users upgrading
from v0.8.4 should use `Search` with caller-owned destination buffers.

The [paired production comparison](production-v082-early-empty-comparison.md)
measures the final search implementation against v0.8.2 using the supplied
96,630-row index, 100 related offers per request and one Local per request.
On the documented M1 Max setup, mixed mean/p99.9 falls from 77.585/235.666 to
32.957/94.125 microseconds. Missing-customer/store/slot means fall from
10.805/14.806/18.128 to 6.592/6.262/4.821 microseconds. Results agree across
both versions. The report includes allocation, retained-memory, Build and
short-request controls, including the one-offer allocation limitation.

Release checks: `go test ./...` and `go test -race ./...` on Go 1.27.3,
golangci-lint v2.12.2 with Go 1.26.0, and `git diff --check`. The linter
requires Go 1.26 because its importer cannot decode Go 1.27 export data.
The release gate also fixes 40 lint findings without suppressions: long declarations
and fixtures are wrapped, benchmark cases are factored into helpers, file close
errors are checked, and nested search/build branches are flattened or extracted.
The Local pool reuse test retries up to 64 times because race instrumentation
may randomly discard a `sync.Pool.Put`, even with garbage collection disabled.
Matching and cache policies remain those of the compared a07b9dd implementation.
Final [diff coverage](experiments/release-0.9.0/diff-coverage.json) is 46/47 changed production statements (97.9%), measured
by intersecting `git diff --unified=0` added-line ranges with the coverage blocks
from `go test -coverprofile=/tmp/ruleix-v090-final.cover ./...`. The release
commit and annotated `v0.9.0` tag are pushed together to origin.

The [final release gate comparison](experiments/release-0.9.0/README.md) checks
all matrix, short and public controls against 453f87d after lint refactoring.
All allocation metrics agree; request means remain comparable.

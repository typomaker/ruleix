# Ruleix v0.10.1 release

Search now returns a bool:

```go
matched := index.Search(conditions, found)
```

The result is true iff this invocation appends at least one match across the
batch. Empty batches and complete misses return false. Existing destination
contents do not count. Every condition is still processed after the first hit,
with unchanged append/order, mismatch-panic and concurrency contracts.

Existing direct calls may discard the return value. Interfaces, method values
and callbacks specifying the old void signature must be updated to return bool.
The Go module path is unchanged. See the [API migration](batch-api-migration.md).

The flag is propagated through existing search execution without added output
scans, snapshots, buffers or shared flags. No new performance claim is made.
Roaring remains v2.26.0. The unfinished dependency experiment is excluded.

## Verification and publication

Production source is exactly the tested 91cb34d implementation. Release
preparation changes documentation only and reuses its successful evidence:

- Full `go test -coverprofile=/tmp/ruleix-search-bool-final.cover ./...`.
- Targeted `go test -race -run 'Test(Search|ShortBatch|BatchOnlyPublic)' ./...`.
- golangci-lint v2.12.2 with Go 1.26.0: zero issues.
- Changed production coverage 38/38 statements (100%); root aggregate 92.9%.

The checks ran with Go 1.27.3 on darwin/arm64, except lint's Go 1.26.0 importer.
Logs and coverage are in [search result evidence](experiments/search-bool-result).
Release-edited files pass whitespace and 500-line checks. No production changes
were made after verification, so checks are not repeated solely for tagging.

The release commit and annotated v0.10.1 tag are pushed atomically to origin.
Publication is verified by comparing remote main and the peeled tag with the
release commit. Existing tags are not moved.

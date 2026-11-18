# Boolean batch Search result

`Index.Search(conditions []C, found [][]ID) bool` returns true exactly when
this call appends a match for at least one condition. Existing destination
contents never count as matches, including IDs already stored in the index.
Empty batches and full misses return false. A length mismatch still panics
before mutation. Every condition is processed after a hit; ordering, appending,
private context ownership and concurrency requirements remain unchanged.

The flag comes from the existing private single-condition search result or
native append sites, then propagates across groups and 128-condition chunks.
Search work remains on the left of boolean OR expressions, so an earlier hit
does not skip later offers or chunks. There are no output snapshots, additional
output scans, new buffers or shared flags. No speedup claim is made.

Calls that discard the result still compile. Method values and interfaces
with the old void signature must change to include the bool result. This extension
ships in [v0.10.1](release-0.10.1.md), following v0.10.0.

## Verification

The differential helper now asserts the return value for every search strategy
and the public API against the existing private scalar kernel. Its existing
cases cover wildcard/lossy/exclusion, comparator roots, native masks and fallback.
Dedicated public cases exercise 0/1/5/8/15/16/100/129/300 conditions with hits in
the first, last or every slot, complete misses and prefilled destinations.
They check full outputs so a true result cannot hide premature termination.
The API surface reflection test verifies the bool method signature.

Commands (Go 1.27.3 on Apple M1 Max / darwin-arm64):

```sh
go test -coverprofile=/tmp/ruleix-search-bool-final.cover ./...
go test -race -run 'Test(Search|ShortBatch|BatchOnlyPublic)' ./...
GOTOOLCHAIN=go1.26.0 /tmp/ruleix-release-tools/golangci-lint run
git diff --check
```

Coverage is intersected with added/modified production statement blocks relative
to pre-task HEAD 7ce06c1 using the migration study's diff-coverage script with
its baseline changed to HEAD. Only search_batch.go and batch_short.go change
in production. Evidence is retained in experiments/search-bool-result.
The paused Roaring evaluation and its staged artifacts are excluded from this
commit; the dependency remains v2.26.0.

All listed checks pass. Changed production coverage is 38/38
statements (100.0%); root aggregate coverage is 92.9%.

# Time range planning within All

Code review at `5e6535b` for a conjunction of an equality Include and a
Between rule. The number of distinct intervals or endpoint keys does not
directly rank the rule. Search planning distinguishes the equality lookup
from selecting the first source of candidate IDs.

- In fresh planning, All obtains cheap equality cardinalities first. An
  equality result of zero ends the search. A cheap bound of at most eight
  skips ordered cardinality estimation and allows direct candidate checks.
- With a broader cheap bound, Between can supply an estimate. Its uncached
  estimate is the minimum of the two bound cardinalities, an upper bound
  on their intersection. Each ordered estimate uses boundary search and
  block prefix counts, with a scan of postings within the boundary block.
  Between can supply the first candidates if its estimate ranks ahead of
  equality. Equal estimates preserve schema order in the cardinality pass.
  The uncached nested All bitmap path can reconsider the initial source
  using estimated work. Local can reuse a validated order and cached
  bitmaps, so a warm search can differ from fresh planning.
- Between stores two ordered endpoint indexes. When materializing, it starts
  with the bound with fewer estimated candidates, favoring the lower bound
  on ties. Range operations visit aggregated bitmaps plus boundary postings.
  Direct checks from an equality candidate test the lower and then the upper
  bound for that ID. Each check can still traverse qualifying bitmap groups.
  A broad range across many distinct endpoints can therefore increase work
  even when equality provided the candidates.

Relevant code: `all_planning.go`, `search_materialization.go`, `all.go`,
`execution_cost.go`, `between.go`, `ordered_index.go`, and
`ordered_index_lookup.go`.

Verification on Darwin arm64 with Go 1.27.3:

```sh
go test ./... -run 'Test(AllFiltersCandidatesThroughBetween|BetweenMatchesScanningReferenceAcrossBlocks|AllAdaptiveExecutorMatchesMaterializeAllOracle|AllUsesWarmOrderedLocalBitmapsDuringPlanning|LocalSearchCachesBetweenNodesWithoutChangingResults)$'
```

The selected tests passed. The mixed equality and Between case checks
search results with 1,000 distinct intervals against a scanning reference
for both Index and Local. The execution-order findings come from code
inspection. Latency and bitmap visits were not measured.

The later [compact Between layout](between-compact-bounds.md) can replace
bitmap group walks in the direct ID path while retaining this plan.

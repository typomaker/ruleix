# Strict equality antonyms

## Build-time model

After bitmap interning, `Build` groups equality children of each `All` by the
identity of their wildcard bitmap. Two groups form an antonym component only
when their wildcard sets are exact complements in the internal ID universe:

```text
cardinality(WA XOR WB) == cardinality(universe)
```

Because every concrete posting is outside its own wildcard, a component with
left concrete postings `L1..Ln` and right concrete postings `R1..Rm` has the
exact query result:

```text
(L1 intersect ... intersect Ln) union (R1 intersect ... intersect Rm)
```

The complement relation therefore produces paired equivalence classes, not an
arbitrary runtime graph: members with the same wildcard form a class, and two
complement classes form one complete bipartite component. `Build` replaces all
of its original operands with one immutable component at the earliest original
position. Overlap, a gap, unequal universes, or a missing complement leaves the
tree unchanged. The compiler first performs an allocation-free existence scan,
so trees without components do not allocate class metadata.

The implementation uses physical equality capabilities only. It never reads
`RuleMode`; Exact, identity-compressed, and compressed Lossy are states of the
same algorithm. The original query-key providers remain attached to the
component, preserving collision-safe `Local` cache validation.

## Search execution

For each side, cardinality is estimated as the smallest concrete posting. Small
physical equality sets scan that posting directly. The bitmap path retains the
first posting as its copy-on-write seed, then intersects the remaining operands
by increasing concrete cardinality. If the first operand is already minimal,
the original loop performs no ordering work. The two side results are united. A 1x1 component
keeps the former direct union fast path, so the accepted pair case does not pay
for the generalized representation.

No search computes wildcard XORs, complements, graph metadata, or mode-specific
branches. Stable repeated `Local` queries hit the component cache with zero
allocations. The cache captures every component query key and includes the key
slice and retained key values in the same child-cache budget as its bitmap.

## Measurements

Baseline: pair implementation `f0b0d68`; candidate: the working tree before
the component commit. Environment: Apple M1 Max, macOS arm64, Go 1.26.0,
`GOMAXPROCS=1`. The fixture contains 4,096 rules and a 3x3 complement
component. The benchmark compares three 1x1 pairs with one full component:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkStrictEqualityAntonymGraph/' \
  -benchmem -benchtime=1s -count=5
```

Medians were:

| Path | Three pairs | One component |
| --- | ---: | ---: |
| `Index` | 4,125 ns, 608 B, 19 allocs | 2,239 ns, 144 B, 5 allocs |
| rotating `Local` | 2,758 ns, 616 B, 23 allocs | 2,467 ns, 240 B, 11 allocs |
| stable `Local` | 143.5 ns, 0 B, 0 allocs | 143.2 ns, 0 B, 0 allocs |
| retained `Local` (`20x`, five runs) | 4,560 B | 3,680 B |

Eight-second CPU profiles showed the pair baseline dominated by repeated
candidate validation, binary search, Roaring add, and intersection work. The
component moves the remaining work to direct equality membership checks and
removes repeated pair materialization. Allocation profiles with
`-memprofilerate=1` confirmed the removal of most Roaring clone/container work.

The production fixture contains no strict component in Exact,
identity-compressed, or Lossy50. Interleaved search A/B against `f0b0d68`
preserved result counts and allocation classes. A longer nine-pair, two-second
Lossy Index check gave medians `32,844 -> 32,802 ns/op`, 1,122 candidates,
70,673 B, and 24 allocations. Exact Index and Local and Lossy Local were
neutral; stable paths remained zero-allocation. Production retained gates
(`20x`, five runs) were unchanged: median about 96,449 B per Lossy Local and
1,322,950 B per Index in both revisions.

Correctness coverage includes 1x1 and asymmetric components, deterministic
3x3 differential checks for Exact and identity-compressed state, independent
components, unknown and missing keys, overlap/gap, empty wildcard, and
`MatchAll`. Final gates are `go test ./...`, `go test -race ./...`, at least
90% changed-production-line coverage, and `git diff --check`.

### Query-time selectivity order

After merging `main`, baseline `a78b647` was compared with merge `a4e2acd`
plus the ordering implementation. The fixture has 8,192 rules and a 4x4
component; postings appear in schema order with about 2,048, 1,024, 586, and
585 IDs per side. Environment is the M1 Max configuration above.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkStrictEqualityAntonymSelectivityOrder/' \
  -benchmem -benchtime=1s -count=5
```

Seven interleaved 1s A/B pairs kept general Index neutral
(`10,623 -> 10,588 ns/op`) with 8,905 B and 6 allocations. With disjoint
smallest postings, ordering stopped after two checks and improved
`9,588 -> 7,829 ns/op`; 8,240 B and 4 allocations were unchanged. Rotating
and stable Local medians were neutral at 240.5/241.7 and 229.2/229.0 ns/op.

An equal-cardinality 4x4 guard initially exposed 2.5% ordering overhead. The
accepted fast path skips ordering whenever the first operand is already
minimal. Seven interleaved 1s A/B pairs then gave neutral medians
`29,751 -> 29,737 ns/op` in five interleaved 3s pairs, with identical 17,610 B
and 8 allocations. Retained state stayed 3,690 B/component Local; production
retained medians stayed about 96,449 B/Lossy Local and 1,322,934 B/Index.

Five-second CPU profiles reproduced early-empty `10,018 -> 8,294 ns/op`; work
remained dominated by Roaring intersection and the selector stayed below 1%
flat CPU. Allocation profiles (`2s`, `-memprofilerate=1`) and per-op classes
were unchanged, as expected from retaining the original copy-on-write seed.

Six alternating-order production pairs (`2s`) were neutral: Exact Index median
`30,317 -> 30,306 ns/op`, Exact Local `229.3 -> 229.7 ns/op`, Lossy Index
`33,063 -> 33,200 ns/op`, and Lossy Local `508.4 -> 503.9 ns/op`; candidates,
bytes, and allocation counts were identical. The 1x1 and compact 3x3 gates were
also neutral in three interleaved 500ms pairs.

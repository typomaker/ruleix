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

For each side, cardinality is estimated as the smallest concrete posting. The
executor starts with that posting and checks the remaining operands in the
side. Small physical equality sets are scanned directly; larger sets use a
pooled bitmap intersection. The two side results are united. A 1x1 component
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

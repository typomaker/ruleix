# `v0.8.3` Local.Search regression profile

Measured on 8 September 2026. The release baseline is `v0.8.2`
(`7f32ddc6ccf6b12419a1efb5af1424ff64353cc0`) and the candidate is `v0.8.3`
(`b9634a90549da35a0c3d6a523a6acc1a1c1891ec`).

## Outcome

The production-shaped warm `Local.Search` regression is in cached query-key
validation, before result-ID materialization. It is primarily caused by the
grouped query-key support added in `f0b0d685`: the rare grouped-provider path
was embedded inside every cache-slot iteration of `loadLocalQueryResult`.
That enlarged the hot generic function and added control flow to ordinary
schemas whose `queryKeyProviders` field is nil.

A smaller component starts at `1508c7c`, which removed exact fixed-arity
equality rules while replacing equality buckets with rounded integer keys.
The neighboring boundary is 0.37% slower. Profiles show the corresponding
shift from unary/binary equality query-key methods to the general `eqRule`
methods, but no single added instruction inside those semantically identical
methods was isolated. The grouped-provider cause has the stronger evidence:
release reproduction, validation-only decomposition, CPU attribution,
introducing-commit blame, reverse A/B, and a semantics-preserving correction
experiment.

## Release reproduction

Nine interleaved process pairs, two seconds each, reproduced the release delta:

| Revision | Median | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `v0.8.2` | 222.4 ns/op | 0 | 0 |
| `v0.8.3` | 225.3 ns/op | 0 | 0 |

The change is **+1.30%**, `p<0.001`. A reverse-order pair of 30-second CPU
profiles independently measured 225.3 and 230.8 ns/op, a 2.44% delta. The
profile processes used `memprofilerate=1`; their absolute latency is therefore
not compared with the ordinary benchmark series.

## Path decomposition

The benchmark normally validates the cached query keys and then copies cached
internal IDs to the caller's result slice. A disposable-worktree experiment
removed only the ID-copy loop. Nine interleaved two-second pairs measured:

| Validation-only path | `v0.8.2` | `v0.8.3` | Change |
| --- | ---: | ---: | ---: |
| warm Local | 189.0 ns/op | 191.6 ns/op | **+1.38%**, `p<0.001` |

The absolute 2.6 ns gap is effectively the full 2.9 ns release gap. Therefore
result materialization is not the cause. A direct materialization-only attempt
was discarded because bypassing key validation selected a cache slot for the
wrong alternating query and changed result cardinality.

Allocation profiles and benchmark counters remained 0 B/op and 0 allocs/op
inside `Local.Search`. The regression is CPU/control-flow work, not allocation.

## CPU profile and compiler evidence

Thirty-second profiles on the same `a78b647` source state compared the original
function with a reverse experiment that removed grouped-provider handling from
the ordinary validation function. Latency changed from 221.6 to 219.6 ns/op.
The normalized differential profile removed 0.53 seconds of flat samples from
`loadLocalQueryResult`; no result-materialization function appeared as added
work.

For the production constraint generic instance, the original and reverse test
binaries reported:

| Property | Original | Reverse |
| --- | ---: | ---: |
| symbol size | 768 bytes | 496 bytes |
| assembly instructions | 192 | 124 |
| compiler inlining cost | 320 | 219 |

Neither shape is inlined, but the larger original function increases the hot
instruction footprint and executes the grouped-versus-ordinary selection
inside the slot loop. Source attribution assigns the `queryKeyProviders` field
and the branch in `loadLocalQueryResult` to `f0b0d685` (`Compile strict
equality antonym pairs`).

## Commit boundaries

Every row used the current production benchmark source in both worktrees.
Primary boundaries used interleaved two-second processes.

| Boundary | Result | Interpretation |
| --- | ---: | --- |
| `v0.8.2 → d62908c` | +0.55%, `p=0.005` | small early component |
| `v0.8.2 → a791a30` | neutral, `p=0.779` | early range narrowed |
| `a791a30 → cae0d03` | neutral, `p=0.538` | equality unification itself is neutral |
| `cae0d03 → 1508c7c` | +0.37%, `p<0.001` | fixed-arity equality removal boundary |
| `1508c7c → d1082cf` | neutral, `p=0.095` | comparable-key rewrite not confirmed |
| `d62908c → a78b647` | +0.95%, `p<0.001` | grouped-provider/removal range |
| `a78b647 → merge 02f95f3` | neutral, `p=0.196` | antonym ordering merge is neutral |
| `02f95f3 → 9a206c2` | neutral, `p=0.805` | aggressive Local policy is neutral |

The side-branch commit `e69c83a` must not be compared directly with
`a78b647`: its parent still contains the failed ID-chunk experiment. The
correct first-parent merge boundary is `861c697 → 02f95f3`, shown above.

## Reverse and correction experiments

On `a78b647`, removing the grouped-provider branch from ordinary validation
changed 221.9 to 220.5 ns/op (**-0.63%**, `p<0.001`, nine interleaved pairs).
This removes functionality and is attribution evidence only.

A second experiment preserved grouped schemas by checking
`queryKeyProviders` once before the slot loop and moving grouped validation to
a separate helper. It changed 222.1 to 220.0 ns/op (**-0.95%**, `p<0.001`) and
passed `GOMAXPROCS=1 go test ./...`. This recovers the complete measured
`d62908c → a78b647` loss.

Applied in a disposable `v0.8.3` worktree, the same helper split changed 221.8
to 220.8 ns/op (**-0.45%**, `p<0.001`) and passed the full test suite. Later
source layout changes alter the magnitude, but the direction and causal hot
operation remain stable. Production code was not changed in this task.

## Commands and environment

- Apple M1 Max, macOS arm64, Go 1.26.0, `GOMAXPROCS=1`;
- detached worktrees and identical `production_shape_benchmark_test.go`;
- `benchstat` from `golang.org/x/perf` revision `19be9d8e6c70`.

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' \
  -benchmem -benchtime=2s -count=1 .

GOMAXPROCS=1 go test -run '^$' \
  -bench '^BenchmarkProductionShapeSearch/Local$' \
  -benchmem -benchtime=30s -count=1 -cpuprofile=local.cpu .

go tool pprof -top -diff_base=original.cpu reverse.cpu
go tool pprof -list=loadLocalQueryResult original.cpu
go tool objdump -s 'loadLocalQueryResult' ruleix.test
go tool nm -size ruleix.test
```

The nine-pair release, validation-only, boundary, reverse, and helper series
alternated process order. A broader five-sample checkpoint scan was used only
to choose direct boundaries; its non-interleaved absolute values are not used
as causal evidence.

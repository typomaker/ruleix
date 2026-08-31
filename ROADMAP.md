# Roadmap

This file is the active implementation plan. Completed work and rejected
experiments belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md);
release-facing behavior belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep
completed, superseded, or unrelated proposals here.

## Objective

Replace coarse universal lossy fallbacks with build-compiled, allocation-free
value codecs and finer equality bucket levels. A named Go type must inherit the
codec of its underlying representation where that representation has a safe,
stable encoding; for example, `type UUID [16]byte` must hash all 16 bytes just
like `[16]byte` without reflection in `Search`.

Retain the current aggregate allocation policy: start with every leaf exact,
then repeatedly apply the available downgrade that releases the most accounted
bytes, breaking ties by larger current leaf usage and stable schema order.
Finer levels add choices to this policy; they do not introduce allocation
rounds or force every leaf lossy before one large leaf can be downgraded again.

After codecs and representation ladders are stable, bound one-pass build
working state opportunistically. `MemoryLimit` remains the hard accounted
retained limit. Internally, a soft build target of 120% of `MemoryLimit`
triggers irreversible streaming downgrades and early release of exact state.
The target is not a promise about Go heap or RSS.

## Non-negotiable contracts

- For every selected representation, `lossy result` is a superset of the exact
  result. False negatives are forbidden.
- `Search` and warm `Local.Search` perform no reflection and introduce no new
  allocation class; UUID and other compiled-codec benchmarks must report
  `0 B/op` and `0 allocs/op` on their warm path.
- Reflection may inspect a type only during `Build`. Published rules retain a
  compiled codec and fixed precision parameters, never a reflective plan that
  calls `reflect.Value` per query.
- A codec always produces its full hash or ordered key. Precision selection is
  completed during `Build`; search only applies the published `bucketCount`,
  reduction parameters, or comparator boundaries.
- Equality hashes every semantic component. A UUID codec mixes all 16 bytes;
  it must never truncate to the first or last `uint64`.
- Raw-memory hashing is allowed only for layouts proven safe, such as `[N]byte`.
  Never hash struct padding, string headers, interface runtime data, or pointer
  bytes as a substitute for their equality semantics.
- Accounting remains architecture-independent and deterministic even when
  build-time reflection discovers a named type's underlying representation.
- The implementation continues to build and test with the module's declared
  Go 1.23 baseline and must not depend on `maphash.Comparable`, which is newer
  than the supported toolchain ceiling discussed for this work.
- `MemoryLimit` continues to describe the final retained representation. The
  internal 120% build target is documented as soft accounted headroom, not a
  physical-memory guarantee.

## Implementation plan

### 1. Freeze codec, precision, and streaming fixtures — completed

- Add type fixtures for ordinary and named `bool`, signed and unsigned
  integers, floats, strings, `[N]byte`, `[2]string`, `[3]int`, comparable
  structs, `type UUID [16]byte`, and a real external UUID type where practical.
- Record exact results, current universal-fallback behavior, accounted bytes,
  selected modes, candidate counts, and warm search allocations.
- Add shuffled-input builds and repeated builds to expose order-dependent
  representation selection before streaming decisions are introduced.
- Add peak-build-memory fixtures that make exact state materially larger than
  the configured retained limit.

Acceptance: every fixture checks the superset property and makes codec
selection, precision selection, build working usage, and search allocations
observable without asserting the old universal fallback as desired behavior.

### 2. Introduce an internal build-compiled equality codec contract — completed

- Separate full-value encoding from precision reduction. A compiled equality
  codec produces a full `uint64`; the selected lossy rule stores only the codec
  plus immutable bucket-reduction parameters.
- Keep direct non-reflective codecs for built-in scalar types.
- When a direct type switch does not match, inspect `reflect.Type` once during
  `Build`. Use `Kind`, `Len`, `Elem`, struct fields, and field offsets to
  recognize the underlying representation of a named type.
- Compile the result into typed or bounded unsafe operations that do not retain
  `reflect.Value` in the search path. Audit each unsafe path with escape
  analysis and architecture tests.
- Return a typed codec-construction error when a safe semantic codec cannot be
  produced. Do not silently select a complete-leaf equality bitmap.

Acceptance: named scalar types choose the same strategy as their underlying
scalar; codec compilation happens once per built leaf; CPU profiles and
benchmarks show no `reflect.*` work during search.

### 3. Compile fixed-byte and recursive comparable codecs

- Recognize every named or unnamed `[N]uint8` by underlying `Array` kind,
  length, and `Uint8` element kind. Hash all `N` bytes with specialized fast
  paths for common lengths such as 8, 16, 20, 24, and 32 and a bounded general
  loop for other lengths.
- Compile arrays of supported elements recursively, including named
  `[2]string` and `[3]int` forms. Include element boundaries or lengths so
  distinct sequences cannot become the same encoded stream accidentally.
- Compile comparable structs from semantic fields in declaration order. Read
  fields through verified offsets and child codecs; do not read padding.
- Define and test equality-compatible handling for floats, complex values,
  pointers, interfaces, and `time.Time`. Leave a category unsupported with a
  clear build error when its stable semantics are not proven.
- Add collision-distribution and throughput benchmarks for ordinary `[16]byte`,
  named UUID, strings, arrays, and representative structs.

Acceptance: `type UUID [16]byte` uses selective hash buckets, hashes both
64-bit halves, has the same collision distribution as `[16]byte`, and remains
allocation-free in search.

### 4. Replace power-of-two-only equality precision with finer bucket counts

- Keep the codec output at full 64-bit precision. Map it to an arbitrary
  immutable `bucketCount` with multiply-high reduction using `bits.Mul64`.
- Start with a bounded ladder of four sublevels per bit interval, for example
  `65536, 57344, 49152, 40960, 32768`, then measure whether adaptive binary
  search over bucket count builds fewer temporary candidates at lower cost.
- Build precision candidates during `Build`; search performs only the compiled
  hash, one reduction, bucket lookup, and bitmap operation.
- Measure actual retained bytes because Roaring and map granularity mean bucket
  count and memory are not perfectly linear.
- Preserve deterministic ordering and deduplicate adjacent candidates that
  retain identical accounted bytes or behavior.

Acceptance: memory and candidates per UUID query change smoothly enough that a
single downgrade no longer necessarily doubles bucket occupancy, without
adding search allocations or planner work to search.

### 5. Revalidate aggregate selective downgrade planning

- Preserve the current per-step selector: maximize bytes released, then larger
  current leaf usage, then schema order.
- At every iteration compare only each leaf's next available downgrade. Permit
  the same large leaf to take several consecutive steps while unrelated leaves
  remain exact.
- Stop immediately when total accounted retained usage fits `MemoryLimit`.
- Add skewed 16-leaf cases proving that finer UUID steps avoid over-compression
  and do not change the exact-retention policy.
- Measure candidate amplification before proposing any quality-per-byte score;
  do not combine a selector change with the codec/precision cutover.

Acceptance: identical inputs select identical representations, no limit is
exceeded, and small fields remain exact whenever their downgrade is not needed.

### 6. Replace universal ordered/composite minima deliberately — completed

- Retain comparator-ordered boundary buckets for arbitrary standalone ordered
  rules; they require no reflection or `int64` projection during search.
- `Between` now evaluates both comparator-bucket sides in one fused node and
  rounds stored bounds outward. Its local query cache avoids repeating the
  broad-side unions on the warm path.
- `CompareBy` now unions operator-specific bucket ranges instead of falling to
  a complete-leaf bitmap and uses the same local-cache admission machinery as
  its exact representation.
- Boundary/operator differential tests enforce the exact-superset contract;
  the production gate retains zero-allocation warm search. The uncached Index
  path remains slower than the old universal minimum and is recorded as an
  accepted selectivity tradeoff in the canonical performance documents.

Acceptance: production `Between[time.Time]` and `CompareBy[[3]int]` retain
useful selectivity under pressure, preserve zero-allocation warm search, and do
not repeat the rejected composed-`Between` regression.

### 7. Add the internal 120% streaming-build headroom

- Compute a saturating internal target as `limit + limit/5`. Keep it private;
  do not add a public option in this phase.
- Track deterministic Ruleix working-state accounting separately from final
  retained accounting. Exclude allocator metadata, input ownership, external
  IDs, and unrelated process heap exactly as documented.
- Check pressure at a fixed insertion interval, initially every 4096 stored
  constraints, rather than after every item.
- When working usage exceeds the target, apply the same next-downgrade selector
  and release exact state that is no longer reachable from any viable plan.
  Route subsequent values directly into the selected streaming accumulator.
- Treat an early downgrade as irreversible for a one-pass iterator. Continue
  downgrading until working accounting returns below the target, then perform
  the ordinary final pass down to `MemoryLimit` after iteration completes.
- Add hysteresis only if measurements show repeated pressure around the target;
  record the threshold and its effect before accepting it.

Acceptance: peak accounted working state is normally brought back below 120%
after each check, final usage stays within 100%, and documentation explicitly
states that Go heap/RSS may exceed both figures.

### 8. Quantify streaming tradeoffs and decide the final contract

- Benchmark exact-first and streaming builds at 10K, 100K, 1M, and available
  larger production sizes. Report build latency, B/op, allocations, peak live
  heap, GC count and pauses, accounted working peak, retained bytes, downgraded
  leaves, candidate amplification, and false-positive rate.
- Repeat ordered and shuffled input. Measure whether irreversible early
  decisions change selected fields or final search quality.
- Profile any build or search regression before revising or removing a
  candidate. Record confirmed causes and unresolved hypotheses in the canonical
  optimization document.
- If the 120% policy is too order-sensitive or cannot materially reduce peak
  heap, keep exact-first as the default and prepare a separate proposal for a
  replayable two-pass input or explicit public build-memory option.

Acceptance: the hard retained contract, soft build-target semantics, measured
quality cost, and order sensitivity are all explicit enough for a caller to
decide whether one-pass streaming behavior is acceptable.

### 9. Finalize public behavior and documentation

- Update `README.md`, `CHANGELOG.md`, architecture, lossy design, optimization
  decisions, and performance history with accepted codecs, precision ladder,
  build headroom, benchmark evidence, and rejected experiments.
- Keep codec selection internal initially. Propose a typed public custom hash
  or ordered-key API only for types that cannot receive a safe compiled codec;
  do not expose reflection or unsafe details in the public contract.
- Move completed steps and measurements to `ROADMAP_HISTORY.md` and leave only
  remaining work in this file.

## Required verification gate

Before accepting every implementation step, run its focused correctness and
allocation tests plus:

```sh
go test ./...
go test -race ./...
GOTOOLCHAIN=go1.23.12 go test ./...
git diff --check
```

For codec and precision changes, run dedicated build/search benchmarks with
ordinary and named UUID types and inspect escape-analysis output. For streaming
changes, run retained-memory and peak-build-memory benchmarks under exact and
lossy production schemas. Every new or changed benchmark must include its
latest local result, machine and Go version, complete dataset/budget parameters,
and reproduction command beside the benchmark and in the relevant canonical
document.

Treat any false negative, hard retained-limit violation, search-time
reflection, new warm allocation, nondeterministic plan, unexplained
order-dependent result, or unprofiled material regression as a failed gate.

# Build-time combination tables for Local

## Decision

The full table and the two-operand variants stopped at the feasibility
gate. Their useful coverage was too low for a bounded, index-derived
table on this workload. The largest modeled budget, 128 KiB, is roughly
5% of the 2.58 MB retained index at checkpoint `3e0fe2c`.
The experiment leaves the production executor at that checkpoint.
There is no candidate timing or p99.9 measurement.

## Workload and exact states

The experiment used the same 96,630-row dump and request generator as
`BenchmarkProductionDumpLocalRequest100`. Each customer gets one Local
and up to 100 offers. The standard 64-request workload yielded 13
requests in the 9–16 candidate cohort. Increasing the seed target to
512 produced 510 unique customers, 122 in the cohort. Both workloads
were evaluated in grouped order and with the existing stride-37 rotation.
Rotation permutes the same offers, so the frequency and coverage totals
are the same. The probe verified the result order of the combined masks against
Local.Search on all 27,000 eligible offer evaluations across both
workloads and orders.

A table is keyed by the exact leading posting and one state per chosen
predicate. An Include state is the membership mask over the ordered
candidate IDs, including wildcard membership and the missing-value
state. The Between domains combine independent lower and upper
endpoint states, including inclusive bounds and wildcards. A compiled
All had 2,252 possible exact leading postings of this size on the dump.
The larger workload used 122 of them. For each sampled posting, five
children have varying masks over the full query domain. The diagnostic
can identify the two range predicates and the store predicate by their
position in this dump. No production logic uses those positions.

## Held-out request coverage

Whole requests were split by seed ordinal parity, then the procedure was
repeated with the halves swapped. The larger split yielded 67 and 55
broad requests, or 6,700 and 5,500 offers. There were 5,004 distinct
full keys across the larger workload. The median key occurred once.
Each customer led with a different posting. The observed frequent keys
therefore had zero held-out hits for both split directions in both
orders at 512 B, 2 KiB, 8 KiB, 32 KiB and 128 KiB. The two-range variant
also had zero held-out hits. This test evaluates a history-derived
admission rule. It leaves open whether traffic with repeated customers
would have better coverage.

## Index-only layouts

The models choose entries solely from the compiled index. They evaluate
all candidate rosters and all states of the chosen predicates. They
compare a dense Cartesian table, packed keys for nonzero
tuples, interned rows with implicit zero, and a decision DAG that drops
constant subtrees and shares identical nodes across rosters. Admission
uses candidate posting order, estimated bytes, or descending
mask width as derived from index states. Diagram ordering includes
compiled operand order, fewest states first, and equality first.

| Combination | Estimated bytes for all eligible rosters | Best coverage at 8 KiB | At 32 KiB | At 128 KiB |
| --- | ---: | ---: | ---: | ---: |
| All predicates | 740,890, shared DAG | 600 / 12,200 | 1,000 / 12,200 | 3,100 / 12,200 |
| Both ranges | 681,441, shared DAG | 600 / 12,200 | 1,800 / 12,200 | 3,700 / 12,200 |
| Slot range and store equality | 351,233, shared DAG | 300 / 12,200 | 2,000 / 12,200 | 6,100 / 12,200 |

The full dense result arrays alone have 22,392,884 cells and cost
44,785,768 B. Only 1,023,847 cells are nonzero, but a packed sparse
key and result layout still costs about 4.34 MB across the catalog.
Interning reduced the all-roster model, but the best full-table model
within 128 KiB covered only about a quarter of sampled broad offers.
The best two-operand model covered about half. An oracle given the
sampled broad roster identities in advance can cover all 122 for
42,902 B of interned slot-range/store rows. The gap comes from
selecting the rosters, even at optimistic byte prices.

The model prices a small roster descriptor, two bytes per distinct mask
label, compact references, and shared rows or states as applicable.
The costs exclude heap and alignment overhead, lookup from a leading
posting to its descriptor, and translation from equality posting
ordinals and range endpoint ranks to roster-local states. The two-byte
DAG reference is also an optimistic layout assumption. Deriving a mask
using the existing execution loop and looking up its state afterward
would leave the expensive membership and endpoint work in the search.
The modeled hit counts therefore serve as an optimistic coverage
screen, with no latency improvement inferred from them.

## Reproduction

Run from the repository with Go 1.27.3 and Python 3. The recorded run
used an Apple M1 Max, macOS 26.6.2, darwin/arm64, GOMAXPROCS=2 and
search code at `3e0fe2c`. The runner overlays the two diagnostic test
files and emits the summaries for both seed counts. It keeps the dump
and intermediate census outside the repository.

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
sh docs/experiments/local-combination-table/run.sh
```

The diagnostic runner, `go test ./...` with the dump enabled,
`go vet ./...`, and `git diff --check` passed.

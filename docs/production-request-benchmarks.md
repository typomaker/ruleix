# Production request benchmark contract

The canonical production-request suite is implemented by
`production_request_*_test.go`; its commands, current measurements, capacity
calculation, and limitations live in
[`benchmark/production-request/README.md`](../benchmark/production-request/README.md).

The suite treats one benchmark operation as an incoming request containing
1, 10, 25, 50, or 100 sequential all-ID searches. It reuses the existing
38,098-constraint production-shaped generator and rotates 2,048 prebuilt
queries. Independent/Correlated and Hot Context/Large Working Set modes expose
request-context reuse and CPU-cache sensitivity. Scaling points are 10K,
38,098, 100K, and 1M constraints. The deterministic mixed workload is explicitly
synthetic and is not described as observed production traffic.

Correctness is a gate, not a timed operation: natural and optimized linear,
handwritten bitmap, Ruleix global Index, and Ruleix Local must return identical
sets for every generated query. Dataset/index construction, query generation,
and result capacity allocation are excluded from matching measurements. The
parallel suite creates one Local or mutable bitmap scratch area per worker.

The load runner schedules matcher-only work at 100–2,000 requests/s and records
achieved throughput, p50/p90/p99/p99.9/max queue-plus-match latency, allocations,
bytes, and GC cycles. It deliberately has no HTTP layer. Portable CPU
utilization is unavailable, so it is not inferred from wall time; the README's
core figure is labeled theoretical CPU-time requirement.

The September 7, 2026 M1 Max series found that Large Working Set at 100
lookups/request missed 700 requests/s on one core for every implementation.
Ruleix Index was fastest in that single-core series (median 4.446 ms/request),
while Ruleix Local was 7.000 ms because 2,048 rotating queries populate rather
than repeatedly hit its query cache. At eight benchmark CPUs, medians were
approximately 1,125 requests/s for Ruleix Index, 1,162 for Ruleix Local, 892 for
the handwritten bitmap baseline, 209 for optimized linear, and 153 for natural
linear. This comparison is synthetic and must not be merged with the observed
production p99.9 number.

OPA v1.13.2 now participates through a prepared Rego collect-all query over the
shared in-memory dataset; conversion, store construction, and compilation are
outside timing. Its first one-lookup checkpoint measured roughly 485 ms and
252 MB/op, so long OPA series remain explicit opt-in runs rather than shrinking
the workload. GoRules/ZEN, Grule, and Casbin remain excluded until an adapter
proves identical all-ID semantics. A reference-only result must be labeled when
an engine's session semantics cannot support a direct comparison.

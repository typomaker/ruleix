# v0.8.2 versus prepared early empty checks

## Revisions and method

Production baseline v0.8.2 resolves to
7f32ddc6ccf6b12419a1efb5af1424ff64353cc0. The accepted portable Go candidate is
a07b9dd04e2680fa51ac30d43b20f38470fce75c. This study changes documentation and
the comparison runner only; production search code is untouched.

Both versions use Go 1.27.3, Apple M1 Max, darwin/arm64,
macOS 26.6.2 and GOMAXPROCS=1. Each retains its released production dependencies,
including Roaring v2.4.4 versus v2.26.0. The temporary v0.8.2 module adds
Google UUID v1.6.0 only for the shared test harness. Test files use a go1.24
build constraint for the modern benchmark loop in the tag's go1.23 module.
This compares complete released implementations, including dependency changes.

All eight test fixture files are byte-identical across both builds, verified
by SHA-256. The supplied schema and dump are unchanged: 96,630 rows, file hash
`ad126179e389201fdacc5b2ab5ff202e76506340ee2d9b3f5207a96d14ec9472`.
The [early-empty study](local-early-empty.md) defines the generator and controls.
A request contains 100 related offers through one Local, followed by Close.
The main generator uses 64 customers; the 1–3/5–8/9–16 cohorts use 16/28/13.

Missing-customer, missing-store and missing-slot controls replace one key with
a value absent from the dump. The index remains fully populated. Existing-key
empty uses present keys yielding no combined matches. Alternating is a stress
control switching missing/present customers inside a Local. It is separate
from the main workload's one-customer-per-request constraint.

Correctness passed on both versions: 32,000 additional empty/recovery offers,
12,800 original grouped/rotated differential offers, and 6,400 zero-candidate
offers in the shared digest fixture. Result counts and SHA-256 match:

```text
Rows=96630 Requests=128 Offers=12800 Matches=1248
0ef4f277cac13b64449e69d6880f0b5a8a7d421670438ec141a20667d9784376
```

## Complete requests

Three 80,000-request samples per release alternate old/current/current/old/old/
current. Tables report medians of per-run metrics, not pooled percentiles.
Latencies are µs per complete 100-offer request, including Local acquisition
and Close. The shared matrix builds one index outside its timed sub-benchmarks.

| Case | Mean, old → current | p99, old → current | p99.9, old → current |
| --- | ---: | ---: | ---: |
| Missing customer | 10.805 → 6.592 | 13.083 → 7.958 | 37.084 → 26.959 |
| Missing store | 14.806 → 6.262 | 17.791 → 6.875 | 43.834 → 21.542 |
| Missing slot type | 18.128 → 4.821 | 21.583 → 5.333 | 51.959 → 20.083 |
| Existing keys, empty intersection | 49.487 → 20.631 | 79.875 → 47.042 | 147.708 → 77.375 |
| Alternating empty/present | 53.185 → 24.508 | 92.458 → 38.916 | 163.083 → 73.000 |
| Mixed | 77.585 → 32.957 | 136.625 → 59.916 | 235.666 → 94.125 |
| 1–3 candidates | 40.668 → 19.884 | 60.834 → 29.584 | 118.250 → 58.708 |
| 5–8 candidates | 92.610 → 33.219 | 130.042 → 51.583 | 223.000 → 80.375 |
| 9–16 candidates | 95.971 → 47.762 | 164.834 → 67.792 | 430.667 → 112.333 |
| Rotated offers | 79.947 → 37.299 | 139.833 → 61.542 | 213.208 → 107.791 |

| Case | B/request, old → current | Allocations/request, old → current |
| --- | ---: | ---: |
| Missing customer | 24 → 24 | 1 → 1 |
| Missing store | 24 → 24 | 1 → 1 |
| Missing slot type | 24 → 24 | 1 → 1 |
| Existing keys, empty intersection | 1979 → 786 | 52 → 10 |
| Alternating empty/present | 2030 → 830 | 52 → 12 |
| Mixed | 3052 → 845 | 89 → 12 |
| 1–3 candidates | 952 → 776 | 11 → 10 |
| 5–8 candidates | 1011 → 819 | 13 → 11 |
| 9–16 candidates | 11159 → 1015 | 389 → 19 |
| Rotated offers | 2866 → 845 | 88 → 12 |

## Short and public-path controls

Mean in µs; allocation metrics are per benchmark operation. Index is a public API control, not the production workload. WarmLocal reuses an already populated Local across requests.

| Control | Mean, old → current | B/op, old → current | Allocations/op, old → current |
| --- | ---: | ---: | ---: |
| PublicControls/Index | 122.868 → 108.975 | 6699 → 6207 | 358 → 341 |
| PublicControls/WarmLocal | 74.831 → 31.726 | 2238 → 6 | 83 → 0 |
| Short/zero/1 | 0.303 → 0.269 | 24 → 24 | 1 → 1 |
| Short/zero/4 | 0.617 → 0.475 | 24 → 24 | 1 → 1 |
| Short/zero/16 | 1.880 → 1.226 | 24 → 24 | 1 → 1 |
| Short/mixed/1 | 3.058 → 2.420 | 983 → 832 | 11 → 12 |
| Short/mixed/4 | 7.833 → 4.273 | 1095 → 835 | 17 → 12 |
| Short/mixed/16 | 18.168 → 8.168 | 1353 → 836 | 26 → 12 |

The one-offer mixed prefix uses 12 allocations versus 11 in v0.8.2, while taking 2.420 versus 3.058 µs and allocating 832 versus 983 bytes. This is a measured limitation of the already accepted implementation; this comparison does not change production code. The 100-offer mixed workload uses 12 versus 89 allocations.

## Build and storage

Two samples per release alternate old/current/current/old. Build/index probes
use 10x and fresh/reopened Local probes use 1000x. These tables average samples.

| Probe | Mean µs, old → current | B/op, old → current | Allocations/op, old → current | Retained B, old → current |
| --- | ---: | ---: | ---: | ---: |
| BuildProbe | 170218.919 → 161264.952 | 4734822.5 → 4787185.0 | 45672.0 → 46370.0 | — |
| RetainedProbe | 169639.408 → 165601.441 | 4737010.0 → 4784972.5 | 45672.0 → 46369.0 | 2586789.5 → 2582574.0 |
| ReopenedLocalRetainedProbe | 76.476 → 34.398 | 3174.5 → 991.5 | 92.0 → 14.5 | 4732.0 → 3431.0 |
| FreshLocal100 | 78.189 → 33.571 | 7183.0 → 3829.0 | 106.0 → 26.0 | — |
| LocalRetainedProbe | 80.151 → 35.054 | 7185.0 → 3829.0 | 106.0 → 26.0 | 4711.0 → 3427.0 |

Build mean falls from 170.219 to 161.265 ms; transient allocation rises from 4,734,823 to 4,787,185 bytes (+1.1%). Retained index memory is approximately 2.59 MB in both. Reopened Local retained memory falls from 4,732 to 3,431 bytes. These small two-sample probes describe storage and construction, not tail latency.

Raw [measurements](experiments/v082-early-empty/measurements.txt), [fixture metadata](experiments/v082-early-empty/metadata.json), and correctness logs are archived beside the runner.

## Reproduction and limits

```sh
export RULEIX_PRODUCTION_DUMP=/absolute/path/to/production-dump.json
python3 docs/experiments/v082-early-empty/run.py
```

The runner archives the exact revisions, copies the same shared fixtures,
builds from each checkout's own directory, verifies fixture hashes and results,
and runs interleaved timing/storage controls. It prints its scratch directory.
Summarize its measurements with:

```sh
python3 docs/experiments/v082-early-empty/summarize.py /scratch/measurements.log
```

The measurements describe this dump-derived workload on a development M1 Max.
The binary composition and matrix harness differ from older full-test-binary
studies; comparisons should use the paired values within this study. Production
frequencies for empty controls were not supplied, so no weighted production
latency or service-wide throughput claim is made.

Verification: both archived revisions compiled; all shared correctness tests and digest checks passed; the runner and summarizer passed Ruff; archived metrics were parsed and sample counts checked; `git diff --check` passed. No production source changed.

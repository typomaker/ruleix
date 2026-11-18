"""Summarize three interleaved samples; never pool per-run tail estimates."""
import json
from pathlib import Path
import statistics
import sys

source = Path(sys.argv[1])
results = {}
for path in sorted(source.glob("measure-*.txt")):
    variant = path.stem.split("-")[-1]
    for line in path.read_text().splitlines():
        if not line.startswith("BenchmarkReleaseComparison/"):
            continue
        fields = line.split()
        cohort = fields[0].split("/")[1]
        values = dict(zip(fields[3::2], map(float, fields[2::2])))
        results.setdefault(cohort, {}).setdefault(variant, []).append(values)
summary = {}
for cohort, variants in results.items():
    assert set(variants) == {"v082", "current"}, cohort
    summary[cohort] = {}
    for variant, samples in variants.items():
        assert len(samples) == 3, (cohort, variant, len(samples))
        summary[cohort][variant] = {
            metric: statistics.median(row[metric] for row in samples)
            for metric in samples[0]
        }
assert len(summary) == 9
(source / "summary.json").write_text(json.dumps(summary, indent=2))
print("| Cohort | Mean old → new, µs | Speedup | p99.9 old → new, µs | B old → new | Allocations old → new |")
print("| --- | ---: | ---: | ---: | ---: | ---: |")
for cohort, variants in summary.items():
    old, new = variants["v082"], variants["current"]
    print(f"| {cohort} | {old['ns/op']/1000:.3f} → {new['ns/op']/1000:.3f} | "
          f"{old['ns/op']/new['ns/op']:.2f}x | "
          f"{old['p99.9-ns/request']/1000:.3f} → {new['p99.9-ns/request']/1000:.3f} | "
          f"{old['B/op']:g} → {new['B/op']:g} | {old['allocs/op']:g} → {new['allocs/op']:g} |")

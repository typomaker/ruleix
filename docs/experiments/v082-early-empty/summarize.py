"""Report medians for timing and means for the separate storage probes."""

import json
import sys
from collections import defaultdict
from pathlib import Path
from statistics import mean, median

samples = defaultdict(list)
version = None
for line in Path(sys.argv[1]).read_text().splitlines():
    if line.startswith(("variant ", "storage ")):
        version = line.split()[1]
    if not line.startswith("Benchmark"):
        continue
    fields = line.split()
    metrics = {"mean_ns": float(fields[2])}
    for metric in (
        "p99-ns/request",
        "p99.9-ns/request",
        "B/op",
        "allocs/op",
        "retained-B/index",
        "retained-B/Local",
    ):
        if metric in fields:
            metrics[metric] = float(fields[fields.index(metric) - 1])
    samples[(version, fields[0])].append(metrics)
result = {}
for (version, benchmark), rows in samples.items():
    aggregate = mean if benchmark.startswith("BenchmarkProductionDump") else median
    result[version + " " + benchmark] = {
        "samples": rows,
        "summary": {key: aggregate(row[key] for row in rows) for key in rows[0]},
    }
print(json.dumps(result, indent=2))

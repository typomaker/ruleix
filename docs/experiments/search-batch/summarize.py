"""Summarize independent metrics by phase and benchmark, without pooling tails."""

import json
import sys
from collections import defaultdict
from pathlib import Path
from statistics import median

root = Path(sys.argv[1])
samples = defaultdict(list)
for path in sorted(root.glob("*.txt")):
    phase = path.stem.split("-")[0]
    if phase not in {"primary", "short", "strategies", "compatibility", "work"}:
        continue
    if phase == "compatibility":
        phase += "-" + path.stem.split("-")[-1]
    for line in path.read_text().splitlines():
        if not line.startswith("Benchmark"):
            continue
        fields = line.split()
        row = {"iterations": int(fields[1])}
        for offset in range(2, len(fields) - 1, 2):
            metric = fields[offset + 1]
            row["mean_ns" if metric == "ns/op" else metric] = float(fields[offset])
        samples[(phase, fields[0])].append(row)
result = {}
for (phase, benchmark), rows in samples.items():
    summary = {key: median(row[key] for row in rows) for key in rows[0]}
    summary["samples"] = len(rows)
    result[phase + " " + benchmark] = summary
print(json.dumps(result, indent=2))

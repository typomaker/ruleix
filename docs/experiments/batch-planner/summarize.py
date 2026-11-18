"""Aggregate complete-run metrics for paired screens and confirmations."""
from pathlib import Path
import json
import statistics
import sys

out = Path(sys.argv[1])
phase = sys.argv[2] if len(sys.argv)>2 else "screen"
rows = {}
for path in sorted(out.glob(phase + "-*.txt")):
    variant = path.stem.split("-")[-1]
    for line in path.read_text().splitlines():
        if not line.startswith("BenchmarkPlanner"):
            continue
        fields = line.split()
        case = "/".join(fields[0].split("/")[1:])
        metrics = dict(zip(fields[3::2], map(float,fields[2::2])))
        rows.setdefault(case, {}).setdefault(variant, []).append(metrics)
summary = {case: {variant: {metric: statistics.median(row[metric] for row in samples)
                           for metric in samples[0]}
                  for variant,samples in variants.items()}
           for case,variants in rows.items()}
(out / (phase+"-summary.json")).write_text(json.dumps(summary,indent=2))
for case,variants in summary.items():
    baseline = "BaseDeep" if "BaseDeep" in variants else "Base"
    if baseline not in variants: continue
    base=variants[baseline]["ns/op"]
    print(case," ".join(f"{v}={d['ns/op']/1000:.3f}({(d['ns/op']/base-1)*100:+.1f}%)" for v,d in variants.items()))

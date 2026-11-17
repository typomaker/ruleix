import re
import statistics
import sys
from collections import defaultdict
from pathlib import Path

samples = defaultdict(list)
for line in Path(sys.argv[1]).read_text().splitlines():
    if re.fullmatch(
        r"(v082-correct|v082|current) (mixed|tiny|medium|broad|rotate)", line
    ):
        key = tuple(line.split())
    if line.startswith("BenchmarkProductionDumpLocalRequest100 "):
        fields = line.split()
        metrics = [float(fields[2])]
        for name in ("p99-ns/request", "p99.9-ns/request", "B/op", "allocs/op"):
            metrics.append(float(fields[fields.index(name) - 1]))
        samples[key].append(metrics)
for key, rows in samples.items():
    medians = [statistics.median(column) for column in zip(*rows)]
    print(
        key,
        "median mean/p99/p999 us, B, alloc",
        [round(v / 1000, 3) for v in medians[:3]] + medians[3:],
        "runs",
        rows,
    )

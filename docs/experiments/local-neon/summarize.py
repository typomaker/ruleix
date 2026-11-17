import re
import statistics
import sys
from collections import defaultdict
from pathlib import Path

samples = defaultdict(list)
key = None
for line in Path(sys.argv[1]).read_text().splitlines():
    if re.fullmatch(
        r"(base|pack-go|pack-neon|fused|cached|optimized-go|selective|selective-cached|selective-fused|fused-value|selective-fused-value|baseline-final|selected-final|selected|verified|final|owner|last|release) (mixed|broad|rotate|tiny|medium)",
        line,
    ):
        key = tuple(line.split())
    if line.startswith("BenchmarkProductionDumpLocalRequest100 "):
        fields = line.split()
        mean = float(fields[2])
        p99 = float(fields[fields.index("p99-ns/request") - 1])
        p999 = float(fields[fields.index("p99.9-ns/request") - 1])
        samples[key].append((mean, p99, p999))
for key, rows in samples.items():
    print(
        key,
        "mean,p99,p999",
        *[round(statistics.mean(x) / 1000, 3) for x in zip(*rows)],
        "runs",
        rows,
    )

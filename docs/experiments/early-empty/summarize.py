import re
import sys
from collections import defaultdict
from pathlib import Path
from statistics import median

r = defaultdict(list)
for line in Path(sys.argv[1]).read_text().splitlines():
    if re.fullmatch(
        r"[a-z0-9-]+ (zero|storezero|slotzero|intersectzero|alternating|mixed|tiny|medium|broad|rotate)",
        line,
    ):
        key = line
    if line.startswith("BenchmarkProductionDumpLocalRequest100 "):
        f = line.split()
        r[key].append(
            [float(f[2])]
            + [
                float(f[f.index(m) - 1])
                for m in ["p99-ns/request", "p99.9-ns/request", "B/op", "allocs/op"]
            ]
        )
for k, v in r.items():
    print(
        k,
        [round(median(x) / 1000, 3) for x in list(zip(*v))[:3]]
        + [median(x) for x in list(zip(*v))[3:]],
        "n=",
        len(v),
    )

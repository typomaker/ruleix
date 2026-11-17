import sys
from collections import defaultdict
from pathlib import Path
from statistics import median

r = defaultdict(list)
for line in Path(sys.argv[1]).read_text().splitlines():
    if line.startswith("variant "):
        variant = line.split()[1]
    if line.startswith("BenchmarkEmptyMatrix/"):
        f = line.split()
        key = (variant, f[0].split("/")[1])
        r[key].append(
            [float(f[2])]
            + [
                float(f[f.index(m) - 1])
                for m in ["p99-ns/request", "p99.9-ns/request", "B/op", "allocs/op"]
            ]
        )
for (v, m), rows in r.items():
    print(
        v,
        m,
        [round(median(x) / 1000, 3) for x in list(zip(*rows))[:3]]
        + [median(x) for x in list(zip(*rows))[3:]],
        "n=",
        len(rows),
    )

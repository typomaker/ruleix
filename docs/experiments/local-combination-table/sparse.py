import collections as C
import functools
import json
import math
import sys
from pathlib import Path


def count_nonempty(domains):
    @functools.lru_cache(None)
    def count(axis, mask):
        if not mask:
            return 0
        if axis == len(domains):
            return 1
        return sum(count(axis + 1, mask & state) for state in domains[axis])

    return count(0, 0xFFFF)


records = [json.loads(s) for s in Path(sys.argv[1]).read_text().splitlines()]
catalog = {r["Roster"]: r for r in records if r["Request"] < 0}
catalog = dict(sorted(catalog.items()))
offers = [r for r in records if r["Request"] >= 0 and not r["Rotate"]]
seen = C.Counter(r["Roster"] for r in offers)
for name, coverage_dims in [
    ("full", tuple(range(6))),
    ("ranges", (0, 1)),
    ("slot-store", (1, 3)),
]:
    models = []
    for i, r in catalog.items():
        domains = [r["Domains"][j] for j in coverage_dims if len(r["Domains"][j]) > 1]
        if len(domains) < 2:
            continue
        domains.sort(key=len)
        suffix = [math.prod(map(len, domains[j:])) for j in range(len(domains) + 1)]

        nonempty = count_nonempty(domains)
        size_key = (max(1, suffix[0] - 1).bit_length() + 7) // 8
        sparse = 8 + sum(2 * len(a) for a in domains) + nonempty * (size_key + 2)
        dense = 8 + sum(2 * len(a) for a in domains) + 2 * suffix[0]
        models.append((i, min(sparse, dense), sparse, nonempty, suffix[0]))
    for subset in (models, [m for m in models if m[0] in seen]):
        print(
            name,
            len(subset),
            "min hybrid, sparse, populated, total states",
            *(sum(m[j] for m in subset) for j in range(1, 5)),
        )
    for name, ranked in [
        ("ordinal", models),
        ("cheapest", sorted(models, key=lambda m: m[1])),
    ]:
        print(name, end=" ")
        for budget in (512, 2048, 8192, 32768, 131072):
            left = budget
            selected = []
            for i, cost, *_ in ranked:
                if cost <= left:
                    left -= cost
                    selected.append(i)
            print((budget, len(selected), sum(seen[i] for i in selected)), end=" ")
        print()

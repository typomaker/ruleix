import collections as C
import json
import sys
from pathlib import Path

records = [json.loads(s) for s in Path(sys.argv[1]).read_text().splitlines()]
catalog = {r["Roster"]: r for r in records if r["Request"] < 0}
catalog = dict(sorted(catalog.items()))
offers = [r for r in records if r["Request"] >= 0 and not r["Rotate"]]
seen = C.Counter(r["Roster"] for r in offers)
budgets = (512, 2048, 8192, 32768, 131072)
for mode in ("ranges", "slot-store", "full"):
    axes = {}
    for i, r in catalog.items():
        # Fixed operand positions are for the diagnostic. Count non-constant axes only.
        candidates = {"ranges": (0, 1), "slot-store": (1, 3), "full": tuple(range(6))}[
            mode
        ]
        indices = [j for j in candidates if len(r["Domains"][j]) > 1]
        if len(indices) >= 2:
            axes[i] = [r["Domains"][j] for j in indices]
    models = {}

    def vector(prefix, widths):
        if not widths:
            yield prefix
        else:
            for m in widths[0]:
                yield from vector(prefix & m, widths[1:])

    def model(a):
        a = sorted(a, key=len)
        rows = C.Counter(tuple(vector(mask, a[:-1])) for mask in a[-1])
        # Duplicate rows can map to one stored row via an ordinal. Pure zero is implicit.
        nonzero = [row for row in rows if any(row)]
        return (
            a,
            nonzero,
            sum(sum(x != 0 for x in row) * n for row, n in rows.items()),
            sum(len(row) * n for row, n in rows.items()),
        )

    for i, a in axes.items():
        if mode == "full" and i not in seen:
            continue
        models[i] = model(a)

    def totals(ids, models=models):
        # Optimistic descriptors and lookup overhead. Sorted mask labels cost 2 B each
        # but translating query coordinates to them is not included.
        descriptor = 0
        shared = set()
        selected = 0
        hit = 0
        for i in ids:
            a, rows, _, _ = models[i]
            descriptor += 8 + sum(2 * len(axis) for axis in a) + 2 * len(a[-1])
            shared.update(rows)
            selected += 1
            hit += seen[i]
        return descriptor + sum(2 * len(row) for row in shared), selected, hit

    print(
        mode,
        "eligible",
        len(axes),
        "ids on sampled offers",
        len(set(axes) & seen.keys()),
    )
    print(" observed compressed oracle", totals(i for i in models if i in seen))
    a = sum(x[2] for x in models.values())
    b = sum(x[3] for x in models.values())
    print(
        " nonzero cells",
        a,
        b,
        "total tuple state count",
        sum(len(r[1]) for r in models.values()),
    )
    if mode == "full":
        continue
    print(" all compressed", totals(models))

    def base_cost(i, models=models):
        a, rows, *_ = models[i]
        return (
            8
            + sum(2 * len(axis) for axis in a)
            + 2 * len(a[-1])
            + sum(2 * len(r) for r in rows)
        )

    for order in ("ordinal", "cheapest", "largest", "oracle"):
        if order == "oracle":
            ranked = sorted((i for i in models if i in seen), key=base_cost)
        else:
            ranked = sorted(
                models,
                key={
                    "ordinal": lambda i: i,
                    "cheapest": base_cost,
                    "largest": lambda i, models=models: (
                        -max(max(a).bit_length() for a in models[i][0]),
                        base_cost(i),
                    ),
                }[order],
            )
        print(order, end=" ")
        for budget in budgets:
            rem = budget
            stored = set()
            hits = count = 0
            for i in ranked:
                a, rows, *_ = models[i]
                extra = set(rows) - stored
                cost = (
                    8
                    + sum(2 * len(axis) for axis in a)
                    + 2 * len(a[-1])
                    + sum(2 * len(row) for row in extra)
                )
                if cost > rem:
                    continue
                stored.update(extra)
                rem -= cost
                count += 1
                hits += seen[i]
            print((budget, count, hits), end=" ")
        print()

import collections as C
import json
import math
import statistics as S
import sys
from pathlib import Path

records = [json.loads(s) for s in Path(sys.argv[1]).read_text().splitlines()]
catalog = {r["Roster"]: r for r in records if r["Request"] < 0}
catalog = dict(sorted(catalog.items()))
budgets = (512, 2048, 8192, 32768, 131072)
print(
    "catalog", len(catalog), "sources", C.Counter(r["Source"] for r in catalog.values())
)
for rot in (False, True):
    offers = [r for r in records if r["Request"] >= 0 and r["Rotate"] == rot]
    seen = {r["Roster"] for r in offers}
    print(
        "rotate",
        rot,
        "requests",
        len({r["Request"] for r in offers}),
        "offers",
        len(offers),
        "rosters",
        len(seen),
    )
    for mode in ("full", "pair", "three"):

        def dimensions(r, mode=mode):
            k = 2 if mode == "pair" else 3
            n = catalog[r]["Counts"]
            if mode == "full":
                return tuple(i for i, x in enumerate(n) if x > 1)
            options = [i for i, x in enumerate(n) if x > 1]
            # Most expensive operands are the two ranges; for ties prefer smaller domains.
            return tuple(sorted(sorted(options, key=lambda i: (i >= 2, n[i], i))[:k]))

        dims = {r: dimensions(r) for r in catalog}
        print(mode, "broad dimensions ranges", C.Counter(dims[r] for r in seen))
        mib = [2 * math.prod(catalog[r]["Counts"][i] for i in dims[r]) for r in catalog]
        chosen = {
            r: 2 * math.prod(catalog[r]["Counts"][i] for i in dims[r]) for r in seen
        }
        print(
            " dense bytes, all catalog",
            sum(mib),
            "observed",
            sum(chosen.values()),
            "observed min/median/max",
            min(chosen.values()),
            S.median(chosen.values()),
            max(chosen.values()),
        )

        def key(x, dims=dims):
            return x["Roster"], tuple(x["Masks"][i] for i in dims[x["Roster"]])

        print(
            " observed unique",
            len({key(r) for r in offers}),
            "repeat counts median",
            S.median(C.Counter(key(r) for r in offers).values()),
        )
        for split in (0, 1):
            train = [r for r in offers if r["Request"] % 2 == split]
            test = [r for r in offers if r["Request"] % 2 != split]
            rk = {r["Roster"] for r in train}
            ranking = C.Counter(key(r) for r in train).most_common()
            print(
                " split",
                split,
                "sizes",
                len(train),
                len(test),
                "shared rosters",
                len(rk & {r["Roster"] for r in test}),
            )
            for budget in budgets:
                selected = set()
                left = budget
                for k, _ in ranking:
                    # 32-bit roster ordinal, 16 bits per masked state and per result.
                    size = 6 + 2 * len(k[1])
                    if size <= left:
                        selected.add(k)
                        left -= size
                n = sum(key(r) in selected for r in test)
                print("  train top key", budget, "entries", len(selected), "hit", n)

import collections as C
import functools
import json
import sys
from pathlib import Path

rows = [json.loads(s) for s in Path(sys.argv[1]).read_text().splitlines()]
catalog = {r["Roster"]: r for r in rows if r["Request"] < 0}
catalog = dict(sorted(catalog.items()))
offers = C.Counter(r["Roster"] for r in rows if r["Request"] >= 0 and not r["Rotate"])
for dimensions in [(0, 1), (1, 3), (0, 1, 3, 4, 5)]:
    for scheme in ("ordered", "small-first", "equality-first"):

        @functools.lru_cache(None)
        def node(a, j, mask):
            if j == len(a) or not mask:
                return mask
            col, domain = a[j]
            edges = tuple(node(a, j + 1, mask & other) for other in domain)
            if all(n == edges[0] for n in edges):
                return edges[0]
            return col, edges

        @functools.lru_cache(None)
        def reachable(root):
            if isinstance(root, int):
                return frozenset()
            return frozenset([root]).union(*(reachable(x) for x in set(root[1])))

        items = []
        for id, r in catalog.items():
            a = tuple(
                (j, tuple(r["Domains"][j]))
                for j in dimensions
                if len(r["Domains"][j]) > 1
            )
            if len(a) < 2:
                continue
            if scheme == "small-first":
                a = tuple(sorted(a, key=lambda x: len(x[1])))
            if scheme == "equality-first":
                a = tuple(sorted(a, key=lambda x: (x[0] < 2, len(x[1]))))
            root = node(a, 0, (1 << 16) - 1)
            nodes = reachable(root)
            cost = 8 + sum(2 * len(axis) for _, axis in a)
            items.append((id, nodes, cost))

        def price(n):
            children = n[1]
            values = C.Counter(children)
            # Use default edge plus exceptions when smaller. Estimate two bytes per reference.
            return 4 + min(
                2 * len(children), 2 + 3 * (len(children) - max(values.values()))
            )

        def size(items):
            return sum(price(n) for n in set().union(*(r[1] for r in items))) + sum(
                r[2] for r in items
            )

        print(
            dimensions,
            scheme,
            "all",
            size(items),
            "sample",
            size([r for r in items if r[0] in offers]),
        )
        for rank in ("ordinal", "cheapest"):
            rs = list(items)
            if rank == "cheapest":
                rs.sort(key=lambda r: r[2] + sum(price(n) for n in r[1]))
            print(rank, end=" ")
            for budget in (8192, 32768, 131072):
                total = hits = 0
                visited = set()
                for id, nodes, desc in rs:
                    delta = nodes - visited
                    charge = desc + sum(price(n) for n in delta)
                    if total + charge > budget:
                        continue
                    total += charge
                    hits += offers[id]
                    visited.update(delta)
                print((budget, hits), end=" ")
            print()

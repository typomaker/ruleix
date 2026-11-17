import json
import sys
from collections import Counter
from pathlib import Path

records = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
for rotate in False, True:
    histograms = {}
    previous = {}
    last_reached = {}
    for row in records:
        if row["Request"] < 0 or row["Rotate"] != rotate:
            continue
        for current in row["Delta"]:
            key = row["Request"], row["Roster"], current["Child"]
            stats = histograms.setdefault(current["Child"], Counter())
            stats["offers"] += 1
            stats["reached"] += current["Reached"]
            adjacent = previous.get(key)
            saved = last_reached.get(key)
            for label, old in (
                ("adjacent", adjacent),
                (
                    "both_reached_adjacent",
                    adjacent
                    if adjacent and adjacent["Reached"] and current["Reached"]
                    else None,
                ),
                ("from_last_reached", saved if current["Reached"] else None),
            ):
                if old is None:
                    continue
                low = abs(current["Lower"] - old["Lower"])
                high = abs(current["Upper"] - old["Upper"])
                events = low + high
                flips = (current["Mask"] ^ old["Mask"]).bit_count()
                stats[(label, "event", min(events, 3))] += 1
                stats[(label, "flips", min(flips, 3))] += 1
                stats[(label, "side_hits")] += (low == 0) + (high == 0)
            previous[key] = current
            if current["Reached"]:
                last_reached[key] = current
    for child, counts in histograms.items():
        print(
            "rotated",
            rotate,
            "child",
            child,
            "offers",
            counts["offers"],
            "reached",
            counts["reached"],
        )
        for label in ("adjacent", "both_reached_adjacent", "from_last_reached"):
            print(
                label,
                "events",
                [counts[label, "event", n] for n in range(4)],
                "flips",
                [counts[label, "flips", n] for n in range(4)],
                "side hits",
                counts[label, "side_hits"],
            )

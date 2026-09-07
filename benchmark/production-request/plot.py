#!/usr/bin/env python3
"""Render an SVG line chart from load-runner CSV without hard-coded results."""

import argparse
import csv
from collections import defaultdict


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("input")
    parser.add_argument("output")
    parser.add_argument("--x", default="lookups_per_request")
    parser.add_argument("--y", default="p999_us")
    parser.add_argument("--series", default="implementation")
    args = parser.parse_args()
    series = defaultdict(list)
    with open(args.input, newline="", encoding="utf-8") as source:
        for row in csv.DictReader(source):
            series[row[args.series]].append((float(row[args.x]), float(row[args.y])))
    points = [point for values in series.values() for point in values]
    if not points:
        raise SystemExit("input contains no rows")
    xmin, xmax = min(x for x, _ in points), max(x for x, _ in points)
    ymin, ymax = 0.0, max(y for _, y in points)
    width, height, pad = 900, 520, 65
    sx = lambda x: pad + (x - xmin) / max(1, xmax - xmin) * (width - 2 * pad)
    sy = lambda y: height - pad - (y - ymin) / max(1, ymax - ymin) * (height - 2 * pad)
    colors = ["#2563eb", "#dc2626", "#059669", "#7c3aed", "#ea580c"]
    svg = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}">',
           '<rect width="100%" height="100%" fill="white"/>',
           f'<path d="M{pad},{pad} V{height-pad} H{width-pad}" fill="none" stroke="#111"/>',
           f'<text x="{width/2}" y="{height-15}" text-anchor="middle">{args.x}</text>',
           f'<text x="18" y="{height/2}" transform="rotate(-90 18 {height/2})" text-anchor="middle">{args.y}</text>']
    for index, (name, values) in enumerate(sorted(series.items())):
        values.sort()
        color = colors[index % len(colors)]
        path = " ".join(("M" if i == 0 else "L") + f"{sx(x):.1f},{sy(y):.1f}" for i, (x, y) in enumerate(values))
        svg.append(f'<path d="{path}" fill="none" stroke="{color}" stroke-width="2"/>')
        svg.append(f'<text x="{width-pad}" y="{pad+18*index}" text-anchor="end" fill="{color}">{name}</text>')
    svg.append("</svg>")
    with open(args.output, "w", encoding="utf-8") as target:
        target.write("\n".join(svg))


if __name__ == "__main__":
    main()

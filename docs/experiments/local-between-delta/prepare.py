"""Prepare isolated Go overlays for the measured Between prototypes."""

import json
import shutil
import subprocess
import sys
from pathlib import Path

fixtures = Path(__file__).resolve().parent
root = fixtures.parents[2]
scratch = Path(sys.argv[1]).resolve()
scratch.mkdir(parents=True, exist_ok=True)
names = ["index_search.go", "local_candidate_mask.go", "local_predicate_projection.go"]
for version in ("base", "candidate", "reuse", "cursor", "separate", "separate-cursor"):
    destination = scratch / version
    destination.mkdir(exist_ok=True)
    overlay = {}
    if version != "base":
        for name in names:
            shutil.copyfile(root / name, destination / name)
        subprocess.run(
            ["patch", "-s", "-p1", "-i", str(fixtures / "scaffold.patch")],
            cwd=destination,
            check=True,
        )
        body = (
            version
            if version in ("candidate", "reuse", "cursor")
            else ("reuse" if version == "separate" else "cursor")
        )
        shutil.copyfile(
            fixtures / f"{body}.go.txt", destination / "local_between_delta.go"
        )
        if version.startswith("separate"):
            subprocess.run(
                ["patch", "-s", "-p1", "-i", str(fixtures / "separate.patch")],
                cwd=destination,
                check=True,
            )
        for source in destination.glob("*.go"):
            overlay[str(root / source.name)] = str(source)
    for source in ("benchmark_test.go.txt",):
        target = destination / "delta_short_bench_test.go"
        shutil.copyfile(fixtures / source, target)
        overlay[str(root / target.name)] = str(target)
    if version != "base":
        target = destination / "delta_focused_test.go"
        shutil.copyfile(fixtures / "focused_test.go.txt", target)
        overlay[str(root / target.name)] = str(target)
    (scratch / f"{version}.json").write_text(json.dumps({"Replace": overlay}))

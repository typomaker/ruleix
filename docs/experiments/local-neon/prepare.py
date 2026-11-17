"""Reconstruct the measured ARM64 prototypes against db3ae77."""

import json
import shutil
import subprocess
import sys
from pathlib import Path

fixtures = Path(__file__).resolve().parent
root = fixtures.parents[2]
scratch = Path(sys.argv[1]).resolve()
scratch.mkdir(parents=True, exist_ok=True)
empty = scratch / "empty.go"
empty.write_text("package ruleix\n")
hidden = [
    "between_mask_arm64.go",
    "between_mask_generic.go",
    "neon_cached.go",
    "neon_cached_test.go",
]
baseline = {}
for name in (
    "index_search.go",
    "local_candidate_mask.go",
    "local_predicate_projection.go",
    "production_dump_workload_test.go",
):
    baseline[name] = subprocess.check_output(
        ["git", "show", f"db3ae77:{name}"],
        cwd=root,
        text=True,
    )
workload = baseline["production_dump_workload_test.go"]
workload = workload.replace(
    '[]string{"narrow", "broad"}',
    '[]string{"narrow", "broad", "tiny", "medium"}',
).replace(
    'if (cohort == "narrow"',
    'if (cohort == "tiny" && size <= 4) || (cohort == "medium" && size >= 5 && size <= 8) || (cohort == "narrow"',
)
baseline["production_dump_workload_test.go"] = workload

variants = ["base"] + [source.stem for source in sorted(fixtures.glob("*.patch"))]
for version in variants:
    destination = scratch / version
    destination.mkdir(exist_ok=True)
    overlay = {str(root / name): str(empty) for name in hidden}
    for name, source in baseline.items():
        (destination / name).write_text(source)
    if version != "base":
        subprocess.run(
            ["patch", "-s", "-p1", "-i", str(fixtures / f"{version}.patch")],
            cwd=destination,
            check=True,
        )
    for source in (fixtures / "common").glob("*.txt"):
        shutil.copyfile(source, destination / source.stem)
    for source in destination.glob("*"):
        if source.suffix in (".go", ".s"):
            overlay[str(root / source.name)] = str(source)
    probe = destination / "neon_probe_test.go"
    shutil.copyfile(fixtures / "probe_test.go.txt", probe)
    overlay[str(root / probe.name)] = str(probe)
    (scratch / f"{version}.json").write_text(json.dumps({"Replace": overlay}))

probe = scratch / "selected_probe_test.go"
shutil.copyfile(fixtures / "probe_test.go.txt", probe)
(scratch / "selected.json").write_text(
    json.dumps(
        {
            "Replace": {str(root / probe.name): str(probe)},
        }
    )
)

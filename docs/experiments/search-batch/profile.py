"""Capture focused CPU/allocation evidence after timing and checks have stopped."""

import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

scratch = Path(sys.argv[1])
output = scratch / "profiles"
output.mkdir(exist_ok=True)
env = {**os.environ, "GOMAXPROCS": "1"}
cases = [
    ("baseline", "mixed", 100, "Local"),
    ("candidate", "mixed", 100, "Local"),
    ("candidate", "mixed", 100, "Full"),
    ("candidate", "mixed", 100, "Staged"),
    ("candidate", "mixed", 100, "Lazy"),
    ("candidate", "mixed", 100, "Candidates"),
    ("candidate", "mixed", 100, "Auto"),
    ("candidate", "unrelated", 100, "Auto"),
    ("candidate", "zero", 2, "Local"),
    ("candidate", "zero", 2, "Auto"),
]
metadata = {
    name: hashlib.sha256((scratch / (name + ".test")).read_bytes()).hexdigest()
    for name in ("baseline", "candidate")
}
(output / "binaries.json").write_text(json.dumps(metadata, indent=2))
for variant, mode, length, strategy in cases:
    name = f"{variant}-{mode}-{length}-{strategy}"
    print(name, flush=True)
    prefix = output / name
    with prefix.with_suffix(".txt").open("w") as stream:
        subprocess.run(
            [
                str(scratch / (variant + ".test")),
                "-test.run",
                "^$",
                "-test.bench",
                f"^BenchmarkProductionBatch$/^{mode}$/^{length}$/^{strategy}$",
                "-test.benchtime=1s",
                "-test.memprofilerate=4096",
                "-test.cpuprofile=" + str(prefix.with_suffix(".cpu")),
                "-test.memprofile=" + str(prefix.with_suffix(".mem")),
            ],
            env=env,
            stdout=stream,
            stderr=subprocess.STDOUT,
            check=True,
        )
    for extension, sample in (("cpu", "cpu"), ("mem", "alloc_space")):
        with (output / (name + "-" + sample + ".txt")).open("w") as stream:
            subprocess.run(
                [
                    "go",
                    "tool",
                    "pprof",
                    "-top",
                    "-nodecount=18",
                    "-sample_index=" + sample,
                    "-focus=searchProductionBatch",
                    str(prefix.with_suffix("." + extension)),
                ],
                stdout=stream,
                stderr=subprocess.STDOUT,
                check=True,
            )

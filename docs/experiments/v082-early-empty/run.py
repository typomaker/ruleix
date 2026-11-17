"""Compare released production code with identical current dump fixtures.

Latest M1 Max / Go 1.27.3 / GOMAXPROCS=1 results: 3 x 80,000 requests,
100 offers per Local; mixed v0.8.2/current mean 77.585/32.957 us and
p99.9 235.666/94.125 us. Full parameters and all controls are recorded in
../../production-v082-early-empty-comparison.md and measurements.txt.
"""

import hashlib
import io
import json
import os
import subprocess
import tarfile
import tempfile
from pathlib import Path

fixtures = Path(__file__).resolve().parents[1] / "early-empty"
repository = fixtures.parents[2]
scratch = Path(tempfile.mkdtemp(prefix="ruleix-v082-empty-")).resolve()
print(scratch, flush=True)
Path("/tmp/ruleix-v082-empty-last").write_text(str(scratch))


def git(*args):
    return subprocess.check_output(["git", *args], cwd=repository)


def run(args, cwd=None, env=None, output=None):
    subprocess.run(
        args, cwd=cwd, env=env, stdout=output, stderr=subprocess.STDOUT, check=True
    )


metadata = {"toolchain": subprocess.check_output(["go", "version"], text=True)}
for name, revision in (("v082", "v0.8.2"), ("current", "a07b9dd")):
    metadata[name] = git("rev-parse", revision + "^{commit}").decode().strip()
    source = scratch / name
    source.mkdir()
    with tarfile.open(fileobj=io.BytesIO(git("archive", revision))) as archive:
        archive.extractall(source, filter="data")
    for path in source.glob("*_test.go"):
        path.unlink()
    for filename in (
        "production_dump_schema_test.go",
        "production_dump_local_differential_test.go",
    ):
        (source / filename).write_bytes(
            b"//go:build go1.24\n\n" + git("show", "a07b9dd:" + filename)
        )
    for fixture, filename in {
        "workload.go.txt": "production_dump_workload_test.go",
        "matrix.go.txt": "production_empty_matrix_test.go",
        "correctness.go.txt": "production_empty_controls_test.go",
    }.items():
        content = (fixtures / fixture).read_bytes()
        if not content.startswith(b"//go:build"):
            content = b"//go:build go1.24\n\n" + content
        (source / filename).write_bytes(content)
    (source / "production_comparison_digest_test.go").write_bytes(
        git("show", "a07b9dd:docs/experiments/production-comparison/digest_test.go.txt")
    )
    (source / "production_comparison_probe_test.go").write_bytes(
        b"//go:build go1.24\n\n"
        + git("show", "a07b9dd:docs/experiments/local-neon/probe_test.go.txt")
    )
    (source / "production_heap_test.go").write_text(
        'package ruleix_test\nimport "runtime"\nfunc heapAlloc() uint64 {var s runtime.MemStats; runtime.ReadMemStats(&s); return s.HeapAlloc}\n'
    )
    if name == "v082":
        run(["go", "mod", "edit", "-require=github.com/google/uuid@v1.6.0"], cwd=source)
    with (scratch / (name + "-build.log")).open("w") as output:
        run(
            [
                "go",
                "test",
                "-mod=mod",
                "-c",
                "-o",
                str(scratch / (name + ".test")),
                ".",
            ],
            cwd=source,
            env={**os.environ, "GOMAXPROCS": "2"},
            output=output,
        )
    metadata[name + "_harness"] = {
        path.name: hashlib.sha256(path.read_bytes()).hexdigest()
        for path in source.glob("*_test.go")
    }
assert metadata["v082_harness"] == metadata["current_harness"]
(scratch / "metadata.json").write_text(json.dumps(metadata, indent=2))
env = {**os.environ, "GOMAXPROCS": "1", "RULEIX_COMPARISON_SMALL": "1"}
for name in ("v082", "current"):
    with (scratch / (name + "-correctness.log")).open("w") as output:
        run(
            [
                str(scratch / (name + ".test")),
                "-test.run",
                "^(TestProductionEmptyControls|TestProductionVersionDigest|TestProductionDumpLocalMatchesIndex)$",
                "-test.v",
            ],
            env={
                **env,
                "RULEIX_COMPARISON_DIGEST": str(scratch / (name + "-digest.json")),
            },
            output=output,
        )
assert (scratch / "v082-digest.json").read_bytes() == (
    scratch / "current-digest.json"
).read_bytes()
with (scratch / "measurements.log").open("w", buffering=1) as output:
    for name in ("v082", "current", "current", "v082", "v082", "current"):
        print("variant", name, file=output, flush=True)
        run(
            [
                str(scratch / (name + ".test")),
                "-test.run",
                "^$",
                "-test.bench",
                "^BenchmarkEmpty(Matrix|Short|PublicControls)$",
                "-test.benchtime=80000x",
                "-test.benchmem",
            ],
            env=env,
            output=output,
        )
    for name in ("v082", "current", "current", "v082"):
        print("storage", name, file=output, flush=True)
        for pattern, iterations in (
            ("^BenchmarkProductionDump(Build|Retained)Probe$", "10x"),
            (
                "^BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe|ReopenedLocalRetainedProbe)$",
                "1000x",
            ),
        ):
            run(
                [
                    str(scratch / (name + ".test")),
                    "-test.run",
                    "^$",
                    "-test.bench",
                    pattern,
                    "-test.benchtime=" + iterations,
                    "-test.benchmem",
                ],
                env=env,
                output=output,
            )
print("Complete:", scratch, flush=True)

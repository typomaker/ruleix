"""Reproduce paired batch-versus-Local measurements from a checkout.

Primary runs: Go 1.27.3 / M1 Max / GOMAXPROCS=1, 3 x 40,000 complete
100-offer requests. See ../../search-batch.md for results and limitations.
"""

import argparse
import hashlib
import io
import json
import os
import platform
import subprocess
import tarfile
import tempfile
import time
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--iterations", type=int, default=40000)
parser.add_argument("--screen", type=int, default=3000)
parser.add_argument("--snapshot", type=Path)
parser.add_argument("--verify-only", action="store_true")
args = parser.parse_args()
repo = Path(__file__).resolve().parents[3]
snapshot = args.snapshot or Path(__file__).with_name("snapshot")


def fixture(name, fallback):
    pinned = snapshot / (name + ".txt")
    return pinned.read_bytes() if pinned.exists() else fallback.read_bytes()


scratch = Path(tempfile.mkdtemp(prefix="ruleix-search-batch-")).resolve()
pointer = (
    "/tmp/ruleix-search-batch-verify-last"
    if args.verify_only
    else "/tmp/ruleix-search-batch-last"
)
Path(pointer).write_text(str(scratch))
print(scratch, flush=True)
base = "d47c5a0"
env = {**os.environ, "GOMAXPROCS": "1"}
metadata = {
    "baseline": subprocess.check_output(
        ["git", "rev-parse", base], cwd=repo, text=True
    ).strip(),
    "go": subprocess.check_output(["go", "version"], text=True).strip(),
    "host": platform.platform(),
    "iterations": args.iterations,
    "screen": args.screen,
    "dump_sha256": hashlib.sha256(
        Path(env["RULEIX_PRODUCTION_DUMP"]).read_bytes()
    ).hexdigest(),
}


def run(command, output, cwd=repo):
    with output.open("w") as stream:
        subprocess.run(
            command,
            cwd=cwd,
            env=env,
            stdout=stream,
            stderr=subprocess.STDOUT,
            check=True,
        )


names = subprocess.check_output(
    ["git", "ls-tree", "-r", "--name-only", base], cwd=repo, text=True
).splitlines()
production = [
    name
    for name in names
    if "/" not in name and name.endswith(".go") and not name.endswith("_test.go")
]
archive = subprocess.check_output(
    ["git", "archive", base, *production, "go.mod", "go.sum"], cwd=repo
)
fixtures = [
    "production_dump_schema_test.go",
    "production_dump_workload_test.go",
    "production_batch_test.go",
    "production_batch_cohort_test.go",
]
old_bridge = """package ruleix
func (ix *Index[C, ID]) SearchBatch(values []C, found [][]ID) {
 local := ix.Local(); defer local.Close()
 for i, value := range values { local.Search(value, &found[i]) }
}
func (ix *Index[C, ID]) SearchBatchVariant(values []C, found [][]ID, _ string) { ix.SearchBatch(values, found) }
func (ix *Index[C, ID]) SearchBatchExperiment(values []C, found [][]ID, _ string) map[string]uint64 {
 ix.SearchBatch(values, found); return nil
}
"""
for variant in ("baseline", "candidate"):
    source = scratch / variant
    source.mkdir()
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        bundle.extractall(source, filter="data")
    if variant == "candidate":
        batch_sources = [
            "search_batch.go",
            "batch_analysis.go",
            "batch_typed.go",
            "batch_context.go",
        ]
        if snapshot.exists():
            batch_sources = [
                path.name.removesuffix(".txt")
                for path in snapshot.glob("*.go.txt")
                if not path.name.endswith("_test.go.txt")
            ]
        for name in batch_sources:
            (source / name).write_bytes(fixture(name, repo / name))
        if not snapshot.exists():
            for name in production:
                (source / name).write_bytes((repo / name).read_bytes())
    for name in fixtures:
        (source / name).write_bytes(fixture(name, repo / name))
    (source / "batch_digest_test.go").write_bytes(
        fixture(
            "batch_digest_test.go", repo / "docs/experiments/search-batch/digest.go.txt"
        )
    )
    bridge = (
        fixture("batch_bridge_test.go", repo / "batch_experiment_test.go").decode()
        if variant == "candidate"
        else old_bridge
    )
    (source / "batch_bridge_test.go").write_text(bridge)
    (source / "batch_heap_test.go").write_text(
        'package ruleix_test\nimport "runtime"\n'
        "func heapAlloc() uint64 {var s runtime.MemStats; runtime.ReadMemStats(&s); return s.HeapAlloc}\n"
    )
    metadata[variant + "_source"] = {
        path.name: hashlib.sha256(path.read_bytes()).hexdigest()
        for path in source.glob("*.go")
    }
    run(
        ["go", "test", "-c", "-o", str(scratch / (variant + ".test")), "."],
        scratch / (variant + "-build.txt"),
        source,
    )
    env["RULEIX_BATCH_DIGEST"] = str(scratch / (variant + "-digest.json"))
    run(
        [
            str(scratch / (variant + ".test")),
            "-test.run",
            "^TestBatchReleaseDigest$",
            "-test.v",
        ],
        scratch / (variant + "-correctness.txt"),
    )
assert (scratch / "baseline-digest.json").read_bytes() == (
    scratch / "candidate-digest.json"
).read_bytes()
for name in fixtures:
    assert metadata["baseline_source"][name] == metadata["candidate_source"][name]
(scratch / "metadata.json").write_text(json.dumps(metadata, indent=2))
if args.verify_only:
    print("Verified:", scratch, flush=True)
    raise SystemExit(0)


def bench(variant, name, pattern, count):
    print(name, flush=True)
    run(
        [
            str(scratch / (variant + ".test")),
            "-test.run",
            "^$",
            "-test.bench",
            pattern,
            "-test.benchtime=" + str(count) + "x",
            "-test.benchmem",
        ],
        scratch / (name + ".txt"),
    )


for process in os.environ.get("RULEIX_BATCH_WAIT_PIDS", "").split():
    print("Waiting for check process", process, flush=True)
    while (
        subprocess.run(
            ["kill", "-0", process],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        ).returncode
        == 0
    ):
        time.sleep(2)

for i, strategy in enumerate(("Local", "Auto", "Auto", "Local", "Local", "Auto"), 1):
    bench(
        "candidate",
        f"primary-{i}-{strategy}",
        f"^BenchmarkProductionBatch$/./^100$/^{strategy}$",
        args.iterations,
    )
for i, strategy in enumerate(("Local", "Auto", "Auto", "Local", "Local", "Auto"), 1):
    bench(
        "candidate",
        f"short-{i}-{strategy}",
        f"^BenchmarkProductionBatch$/^(mixed|zero|unrelated|partial|identical)$/^(0|1|2|3|4|16)$/^{strategy}$",
        20000,
    )
strategies = (
    "Local",
    "Common",
    "Full",
    "Staged",
    "Lazy",
    "Subgroups",
    "Candidates",
    "Typed",
)
for repeat, order in enumerate((strategies, tuple(reversed(strategies))), 1):
    for strategy in order:
        bench(
            "candidate",
            f"strategies-{repeat}-{strategy}",
            f"^BenchmarkProductionBatch$/./^100$/^{strategy}$",
            args.screen,
        )
for i, variant in enumerate(("baseline", "candidate", "candidate", "baseline"), 1):
    bench(
        variant,
        f"compatibility-{i}-{variant}",
        "^BenchmarkProductionBatch$/^(mixed|zero|unrelated)$/^100$/^Local$",
        20000,
    )
env["RULEIX_BATCH_WORK"] = "1"
bench(
    "candidate",
    "work",
    "^BenchmarkProductionBatch$/^(mixed|zero|intersectzero|unrelated|partial)$/^100$",
    1,
)
print("Complete:", scratch, flush=True)

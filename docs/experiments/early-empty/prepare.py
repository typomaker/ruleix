"""Prepare canonical temporary checkouts and identical benchmark fixtures."""

import hashlib
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

fixtures = Path(__file__).resolve().parent
repository = fixtures.parents[2]
scratch = Path(tempfile.mkdtemp(prefix="ruleix-early-empty-")).resolve()
source = scratch / "source"
source.mkdir()


def git(*args):
    return subprocess.check_output(["git", *args], cwd=repository)


with tarfile.open(fileobj=io.BytesIO(git("archive", "5f6e63e"))) as archive:
    archive.extractall(source, filter="data")
keep = {
    "production_dump_workload_test.go",
    "production_dump_schema_test.go",
    "production_dump_local_differential_test.go",
}
for path in source.glob("*_test.go"):
    if path.name not in keep:
        path.unlink()
for fixture, target in {
    "workload.go.txt": "production_dump_workload_test.go",
    "matrix.go.txt": "production_empty_matrix_test.go",
    "correctness.go.txt": "production_empty_controls_test.go",
    "layout.go.txt": "experiment_layout_test.go",
}.items():
    (source / target).write_bytes((fixtures / fixture).read_bytes())
(source / "production_comparison_digest_test.go").write_bytes(
    git("show", "5f6e63e:docs/experiments/production-comparison/digest_test.go.txt")
)
(source / "production_comparison_probe_test.go").write_bytes(
    b"//go:build go1.24\n\n"
    + git("show", "5f6e63e:docs/experiments/local-neon/probe_test.go.txt")
)
(source / "production_heap_test.go").write_text(
    'package ruleix_test\nimport "runtime"\n'
    "func heapAlloc() uint64 {var s runtime.MemStats; "
    "runtime.ReadMemStats(&s); return s.HeapAlloc}\n"
)
for script in ("variants", "refinements", "specialize", "capture", "scope"):
    subprocess.run(
        [sys.executable, str(fixtures / (script + ".py")), str(scratch)],
        check=True,
        stdout=subprocess.DEVNULL,
    )
revision = os.environ.get("RULEIX_EMPTY_CANDIDATE", "HEAD")
selected = scratch / "accepted"
selected.mkdir()
paths = (
    "all.go",
    "all_planning.go",
    "all_empty_order.go",
    "all_empty_ranking.go",
    "index_search.go",
    "local_candidate_mask.go",
    "local_empty_operand.go",
    "local_empty_keys.go",
)
for name in paths:
    (selected / name).write_bytes(
        (repository / name).read_bytes()
        if revision == "WORKTREE"
        else git("show", revision + ":" + name)
    )
(scratch / "accepted.json").write_text(
    json.dumps(
        {"Replace": {str(source / name): str(selected / name) for name in paths}}
    )
)
(scratch / "revisions.txt").write_text(
    "baseline "
    + git("rev-parse", "5f6e63e").decode()
    + "candidate "
    + ("WORKTREE on " if revision == "WORKTREE" else "")
    + git("rev-parse", "HEAD" if revision == "WORKTREE" else revision).decode()
    + subprocess.check_output(["go", "version"], text=True)
)
(scratch / "source-hashes.json").write_text(
    json.dumps(
        {
            str(path.relative_to(scratch)): hashlib.sha256(
                path.read_bytes()
            ).hexdigest()
            for directory in (source, selected)
            for path in directory.glob("*.go")
        },
        indent=2,
    )
)
print(scratch)

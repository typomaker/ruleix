"""Compare acaa3db SearchBatch with the batch-only Search API.

GOMAXPROCS=1; three interleaved 40,000-request primary samples, followed by
short-batch controls. Set RULEIX_PRODUCTION_DUMP to the full production JSON.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile

repo = Path(__file__).resolve().parents[3]
out = Path(tempfile.mkdtemp(prefix="ruleix-batch-api-")).resolve()
Path("/tmp/ruleix-batch-api-last").write_text(str(out))
print(out, flush=True)
env = {**os.environ, "GOMAXPROCS": "1"}
base = "acaa3db"


def git(*args):
    return subprocess.check_output(["git", *args], cwd=repo)


def run(command, name, cwd=repo):
    with (out / name).open("w") as stream:
        subprocess.run(command, cwd=cwd, env=env, stdout=stream,
                       stderr=subprocess.STDOUT, check=True)


names = git("ls-tree", "--name-only", base).decode().splitlines()
production = [n for n in names if n.endswith(".go") and not n.endswith("_test.go")]
archive = git("archive", base, *production, "go.mod", "go.sum")
fixtures = ["production_dump_schema_test.go", "production_dump_workload_test.go",
            "production_batch_test.go", "production_batch_cohort_test.go"]
metadata = {"baseline": git("rev-parse", base).decode().strip(),
            "candidate_parent": git("rev-parse", "HEAD").decode().strip(),
            "go": subprocess.check_output(["go", "version"], text=True).strip(),
            "platform": platform.platform(), "primary_iterations": 40000,
            "dump_sha256": hashlib.sha256(Path(env["RULEIX_PRODUCTION_DUMP"]).read_bytes()).hexdigest()}
for variant in ("baseline", "candidate"):
    source = out / variant
    source.mkdir()
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        bundle.extractall(source, filter="data")
    if variant == "candidate":
        for path in repo.glob("*.go"):
            if not path.name.endswith("_test.go"):
                (source / path.name).write_bytes(path.read_bytes())
        (source / "legacy_search_test.go").write_bytes((repo / "legacy_search_test.go").read_bytes())
    for name in fixtures:
        text = git("show", f"{base}:{name}").decode()
        if variant == "candidate":
            text = text.replace(".Search(", ".SearchOne(").replace(".Local()", ".TestLocal()")
            text = text.replace("ruleix.Local[", "ruleix.TestContext[").replace(".SearchBatch(", ".Search(")
        (source / name).write_text(text)
    (source / "batch_experiment_test.go").write_bytes(git("show", f"{base}:batch_experiment_test.go"))
    (source / "batch_digest_test.go").write_bytes((repo / "docs/experiments/search-batch/digest.go.txt").read_bytes())
    (source / "batch_heap_test.go").write_text('package ruleix_test\nimport "runtime"\n'
        'func heapAlloc() uint64 {var s runtime.MemStats; runtime.ReadMemStats(&s); return s.HeapAlloc}\n')
    metadata[variant + "_source_sha256"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                            for p in source.glob("*.go")}
    run(["go", "test", "-c", "-o", str(out / (variant + ".test")), "."], variant + "-build.txt", source)
    env["RULEIX_BATCH_DIGEST"] = str(out / (variant + "-digest.json"))
    run([str(out / (variant + ".test")), "-test.run", "^TestBatchReleaseDigest$", "-test.v"], variant + "-digest.txt")
assert (out / "baseline-digest.json").read_bytes() == (out / "candidate-digest.json").read_bytes()
(out / "metadata.json").write_text(json.dumps(metadata, indent=2))
for group, pattern, count, order in [
    ("primary", "^BenchmarkProductionBatch$/^(mixed|zero|storezero|slotzero|intersectzero|tiny|broad|unrelated)$/^100$/^Auto$", 40000,
     ["baseline", "candidate", "candidate", "baseline", "baseline", "candidate"]),
    ("empty-confirm", "^BenchmarkProductionBatch$/^(storezero|intersectzero)$/^100$/^Auto$", 400000,
     ["baseline", "candidate", "candidate", "baseline", "baseline", "candidate"]),
    ("short", "^BenchmarkProductionBatch$/^(mixed|zero|storezero|slotzero|intersectzero|unrelated)$/^(0|1|4|16|100)$/^Auto$", 20000,
     ["baseline", "candidate", "candidate", "baseline"]),
]:
    for i, variant in enumerate(order):
        name = f"{group}-{i}-{variant}.txt"
        print(name, flush=True)
        run([str(out / (variant + ".test")), "-test.run", "^$", "-test.bench", pattern,
             f"-test.benchtime={count}x", "-test.benchmem"], name)
for variant in ("baseline", "candidate"):
    run([str(out / (variant + ".test")), "-test.run", "^$", "-test.bench",
         "^BenchmarkProductionBatch$/^intersectzero$/^100$/^Auto$",
         "-test.benchtime=1000000x", "-test.cpuprofile=" + str(out / (variant + ".cpu")),
         "-test.memprofile=" + str(out / (variant + ".mem"))], "profile-" + variant + ".txt")
    for kind, flags in [("cpu", []), ("mem", ["-alloc_space"])]:
        run(["go", "tool", "pprof", "-top", *flags, str(out / (variant + ".test")),
             str(out / (variant + "." + kind))], f"profile-{variant}-{kind}-top.txt")
print("Verified:", out, flush=True)

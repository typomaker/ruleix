"""Compare released v0.8.2 Local.Search with 8ec5e05 batch Search.

Latest M1 Max / Go 1.27.3 / GOMAXPROCS=1, 3 x 80,000 requests: mixed
v0.8.2/current mean 74.605/12.350 us, p99.9 170.542/30.041 us;
3,014/0 B and 87/0 allocations per complete request. One customer, 100 related
offers, one Local per old request. See ../../production-v082-batch-comparison.md.
Run alone with RULEIX_PRODUCTION_DUMP set to the full JSON.
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

fixtures = Path(__file__).resolve().parent
repo = fixtures.parents[2]
out = Path(tempfile.mkdtemp(prefix="ruleix-v082-batch-")).resolve()
Path("/tmp/ruleix-v082-batch-last").write_text(str(out))
print(out, flush=True)
env = {**os.environ, "GOMAXPROCS": "1"}


def git(*args):
    return subprocess.check_output(["git", *args], cwd=repo)


def run(command, name, cwd=repo):
    with (out / name).open("w") as stream:
        subprocess.run(command, cwd=cwd, env=env, stdout=stream,
                       stderr=subprocess.STDOUT, check=True)


metadata = {"go": subprocess.check_output(["go", "version"], text=True).strip(),
            "platform": platform.platform(), "gomaxprocs": 1,
            "iterations": 80000, "samples_per_revision": 3,
            "dump_sha256": hashlib.sha256(Path(env["RULEIX_PRODUCTION_DUMP"]).read_bytes()).hexdigest()}
for variant, revision in [("v082", "v0.8.2"), ("current", "8ec5e05")]:
    source = out / variant
    source.mkdir()
    names = git("ls-tree", "--name-only", revision).decode().splitlines()
    production = [n for n in names if n.endswith(".go") and not n.endswith("_test.go")]
    with tarfile.open(fileobj=io.BytesIO(git("archive", revision, *production, "go.mod", "go.sum"))) as bundle:
        bundle.extractall(source, filter="data")
    for name, content in {
        "schema": git("show", "8ec5e05:production_dump_schema_test.go"),
        "cohort": git("show", "8ec5e05:production_batch_cohort_test.go"),
        **{p.stem.removesuffix(".go"): p.read_bytes() for p in fixtures.glob("*.go.txt")},
    }.items():
        (source / (name + "_test.go")).write_bytes(b"//go:build go1.24\n\n" + content)
    adapter = """package ruleix_test
import (
 "github.com/google/uuid"
 "github.com/typomaker/ruleix"
)
func searchComparison(index *ruleix.Index[productionDumpConstraint,uuid.UUID],
 values []productionDumpConstraint, found [][]uuid.UUID) {
 BODY
}
"""
    body = "index.Search(values,found)"
    if variant == "v082":
        body = "local:=index.Local(); defer local.Close(); for i,value:=range values { local.Search(value,&found[i]) }"
        run(["go", "mod", "edit", "-require=github.com/google/uuid@v1.6.0"], variant + "-uuid.txt", source)
    (source / "adapter_test.go").write_text(adapter.replace("BODY", body))
    run(["gofmt", "-w", *[p.name for p in source.glob("*_test.go")]], variant + "-gofmt.txt", source)
    metadata[variant] = {"revision": git("rev-parse", revision + "^{commit}").decode().strip(),
                        "fixtures_sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                            for p in source.glob("*_test.go")}}
    run(["go", "test", "-mod=mod", "-c", "-o", str(out / (variant + ".test")), "."], variant + "-build.txt", source)
    metadata[variant]["dependencies"] = subprocess.check_output(
        ["go", "list", "-m", "all"], cwd=source, env=env, text=True).splitlines()
    env["RULEIX_COMPARISON_DIGEST"] = str(out / (variant + "-digest.json"))
    run([str(out / (variant + ".test")), "-test.run", "^TestReleaseComparison$", "-test.v"],
        variant + "-correctness.txt", source)
assert (out / "v082-digest.json").read_bytes() == (out / "current-digest.json").read_bytes()
old = dict(metadata["v082"]["fixtures_sha256"])
new = dict(metadata["current"]["fixtures_sha256"])
del old["adapter_test.go"], new["adapter_test.go"]
assert old == new
(out / "metadata.json").write_text(json.dumps(metadata, indent=2))
for i, variant in enumerate(["v082", "current", "current", "v082", "v082", "current"]):
    name = f"measure-{i}-{variant}.txt"
    print(name, flush=True)
    run([str(out / (variant + ".test")), "-test.run", "^$", "-test.bench",
         "^BenchmarkReleaseComparison$", "-test.benchtime=80000x", "-test.benchmem"], name)
print("Verified:", out, flush=True)

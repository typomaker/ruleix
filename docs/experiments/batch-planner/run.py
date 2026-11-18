"""Build pinned isolated planner experiments and run interleaved screens.

No normal-build mode flags are introduced. Use --phase build, screen, short,
shapes or confirm; the persistent scratch directory is printed and recorded.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import tempfile
import sys
sys.dont_write_bytecode = True
import variants

parser = argparse.ArgumentParser()
parser.add_argument("--phase", choices=["build", "screen", "short", "shapes", "confirm"], default="build")
parser.add_argument("--variants", default=",".join(variants.VARIANTS))
parser.add_argument("--scratch", type=Path)
parser.add_argument("--iterations", type=int)
parser.add_argument("--label")
args = parser.parse_args()
here = Path(__file__).resolve().parent
repo = here.parents[2]
base = "053c15c"
selected = args.variants.split(",")
out = args.scratch or Path(tempfile.mkdtemp(prefix="ruleix-batch-planner-")).resolve()
Path("/tmp/ruleix-batch-planner-last").write_text(str(out))
print(out, flush=True)
env = {**os.environ, "GOMAXPROCS": "1"}


def git(*command):
    return subprocess.check_output(["git", *command], cwd=repo)


def run(command, name, cwd=repo, overrides=None):
    print(name, flush=True)
    with (out / name).open("w") as stream:
        subprocess.run(command, cwd=cwd, env={**env, **(overrides or {})},
                       stdout=stream, stderr=subprocess.STDOUT, check=True)


if args.phase == "build":
    names = git("ls-tree", "--name-only", base).decode().splitlines()
    production = [n for n in names if n.endswith(".go") and not n.endswith("_test.go")]
    archive = git("archive", base, *production, "go.mod", "go.sum")
    prior = here.parent / "v082-batch-search"
    metadata = {"baseline": git("rev-parse", base).decode().strip(),
                "go": subprocess.check_output(["go", "version"], text=True).strip(),
                "host": platform.platform(), "gomaxprocs": 1,
                "dump_sha256": hashlib.sha256(Path(env["RULEIX_PRODUCTION_DUMP"]).read_bytes()).hexdigest()}
    if (out / "metadata.json").exists(): metadata.update(json.loads((out / "metadata.json").read_text()))
    for name in selected:
        assert name in variants.VARIANTS
        source = out / name
        if source.exists(): shutil.rmtree(source)
        source.mkdir()
        with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
            bundle.extractall(source, filter="data")
        if name in ("AcceptedDeep", "Accepted2Deep", "Accepted3Deep", "Accepted4Deep", "AcceptedShortDeep"):
            snapshot = here / {"AcceptedDeep":"accepted-initial","Accepted2Deep":"accepted-wrapper","Accepted3Deep":"accepted-fat-map","Accepted4Deep":"accepted","AcceptedShortDeep":"accepted-short"}[name]
            if snapshot.exists():
                for path in snapshot.glob("*.go.txt"):
                    (source / path.name.removesuffix(".txt")).write_bytes(path.read_bytes())
            else:
                for path in repo.glob("*.go"):
                    if not path.name.endswith("_test.go"): (source / path.name).write_bytes(path.read_bytes())
        else:
            variants.prepare(source, name)
        for filename in ["production_dump_schema_test.go", "production_batch_cohort_test.go"]:
            (source / filename).write_bytes(git("show", base + ":" + filename))
        for filename in ["generator", "predicate-miss"]:
            (source / (filename + "_test.go")).write_bytes((prior / (filename + ".go.txt")).read_bytes())
        text = (prior / "comparison.go.txt").read_text()
        text = text[:text.index("// M1 Max, Go 1.27.3")]
        # imports used only by the replaced benchmark are removed below.
        text = text.replace('\n\t"time"', '\n\t"time"')
        text = text.replace('"predicatezero"}', '"predicatezero", "unrelated", "identical", "rotate", "partial"}')
        # The formatted source's mode list ends on the same line; assert below.
        text = text.replace('"predicatezero"\n}', '"predicatezero", "unrelated", "identical", "rotate", "partial"\n}')
        text = text.replace('if mode == "predicatezero" {', '''if mode == "identical" {
 for _,request:=range requests {for i:=range request {request[i]=request[0]}};return requests
 }
 if mode=="rotate" {for _,request:=range requests {old:=slices.Clone(request);for i:=range request {request[i]=old[i*37%len(old)]}};return requests}
 if mode=="unrelated"||mode=="partial" {
  original:=make([][]productionDumpConstraint,len(requests));for i:=range requests {original[i]=slices.Clone(requests[i])}
  for r,request:=range requests {for i:=range request {buyer:=(r+i)%len(original);if mode=="partial" {buyer=(r+i%4)%len(original)};request[i]=original[buyer][i*37%len(request)]}};return requests
 }
 if mode == "predicatezero" {''', 1)
        text = text.replace('checkRequest(t, request)', 'if mode!="unrelated"&&mode!="partial"&&mode!="identical"&&mode!="rotate" {checkRequest(t,request)}')
        assert '"unrelated", "identical", "rotate", "partial"' in text
        (source / "comparison_test.go").write_text(text)
        for filename in ["benchmark", "shapes", "probe"]:
            (source / (filename + "_test.go")).write_bytes((here / (filename + ".go.txt")).read_bytes())
        if name in ("BaseFinalDeep", "AcceptedDeep", "Accepted2Deep", "Accepted3Deep", "Accepted4Deep", "AcceptedShortDeep"):
            for filename in ["storage", "layout"]:
                (source / (filename+"_test.go")).write_bytes((here / (filename+".go.txt")).read_bytes())
        if name.endswith("Deep"):
            (source / "large_key_test.go").write_bytes((here / "large-key.go.txt").read_bytes())
            shape = (source / "shapes_test.go").read_text()
            shape = shape.replace('rows[i] = plannerShape', 'rows[i] = plannerShape')
            shape = shape.replace('schema:=ruleix.All(', 'if mode=="compiled"||mode=="compiledexclusion" {for i:=range rows {rows[i].from=i%2;rows[i].until=20+i%3}}\n schema:=ruleix.All(')
            shape = shape.replace('var inspector ruleix.Inspector', 'if mode=="compiledexclusion" {schema=ruleix.All(schema,ruleix.Exclude(func(v plannerShape)(int,bool){return v.store,true}))}\n var inspector ruleix.Inspector')
            shape = shape.replace('"native","observed","lossy"', '"native","compiled","compiledexclusion","observed","lossy"')
            shape = shape.replace('"native","observed"', '"native","compiled","compiledexclusion","observed"')
            shape = shape.replace('if row.group==q.group', 'if mode!="compiledexclusion" && row.group==q.group')
            if name in ("BaseFinalDeep", "AcceptedDeep", "Accepted2Deep", "Accepted3Deep", "Accepted4Deep", "AcceptedShortDeep"):
                # Native Between needs >=256 IDs and at least eight bounds.
                shape = shape.replace('rows,ids:=make([]plannerShape,4*count),make([]int,4*count)', 'groups:=4;if mode=="compiled"||mode=="compiledexclusion" {groups=max(groups,(256+count-1)/count)};rows,ids:=make([]plannerShape,groups*count),make([]int,groups*count)')
                shape = shape.replace('if mode=="compiled"||mode=="compiledexclusion" {for i:=range rows {rows[i].from=i%2;rows[i].until=20+i%3}}', '')
                shape = shape.replace('return index,rows,ids', 'if mode=="compiled" {require.True(t,index.PlannerNative())};if mode=="compiledexclusion" {require.False(t,index.PlannerNative())};return index,rows,ids')
                shape = shape.replace('ruleix.Exclude(func(v plannerShape)(int,bool){return v.store,true})', 'ruleix.Exclude(func(v plannerShape)(int,bool){return v.until,true})')
                shape = shape.replace('if mode!="compiledexclusion" && row.group==q.group', 'if (mode!="compiledexclusion"||row.until!=q.until) && row.group==q.group')
                shape = shape.replace('"native"','"original"')
            (source / "shapes_test.go").write_text(shape)

        (source / "adapter_test.go").write_text('''package ruleix_test
import("github.com/google/uuid";"github.com/typomaker/ruleix")
func searchComparison(index *ruleix.Index[productionDumpConstraint,uuid.UUID],values []productionDumpConstraint,found [][]uuid.UUID) {index.Search(values,found)}
''')
        run(["gofmt", "-w", *[p.name for p in source.glob("*.go")]], name + "-gofmt.txt", source)
        run(["go", "test", "-c", "-o", str(out / (name + ".test")), "."], name + "-build.txt", source)
        run([str(out / (name + ".test")), "-test.run", "^Test(ReleaseComparison|PlannerShapes|PlannerProbe|PlannerLargeKey)$", "-test.v"],
            name + "-correctness.txt", overrides={"RULEIX_COMPARISON_DIGEST": str(out / (name + "-digest.json")),
                                                  "RULEIX_TRIAL_PROBE": str(out / (name + "-probe.json"))})
        metadata[name] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in source.glob("*.go")}
        if name != "Base":
            assert (out / "Base-digest.json").read_bytes() == (out / (name + "-digest.json")).read_bytes()
        for variant in list(metadata):
            if variant in variants.VARIANTS and isinstance(metadata[variant], dict):
                manifest = "source-" + variant + ".json"
                (out / manifest).write_text(json.dumps(metadata[variant], indent=2))
                metadata[variant] = manifest
        (out / "metadata.json").write_text(json.dumps(metadata, indent=2))
    raise SystemExit(0)

count = args.iterations or (40000 if args.phase == "confirm" else 5000)
for turn, order in enumerate([selected, list(reversed(selected))] if args.phase != "confirm" else [selected, list(reversed(selected)), selected]):
    for name in order:
        overrides = {}
        pattern = "^BenchmarkPlannerProduction$"
        if args.phase == "short":
            overrides = {"RULEIX_TRIAL_MODES": "mixed,zero,storezero,slotzero,intersectzero,predicatezero,unrelated,identical",
                         "RULEIX_TRIAL_LENGTHS": "1,4,5,6,8,10,12,15,16"}
        elif args.phase == "shapes":
            pattern = "^BenchmarkPlanner(Shapes|LargeKey)$"
        run([str(out / (name + ".test")), "-test.run", "^$", "-test.bench", pattern,
             f"-test.benchtime={count}x", "-test.benchmem"], f"{args.label or args.phase}-{turn}-{name}.txt", overrides=overrides)

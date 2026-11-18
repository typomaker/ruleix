"""Copy audit evidence and split summaries into <=500-line JSON files."""
import json
from pathlib import Path
import shutil
import sys

source=Path(sys.argv[1])
target=Path(__file__).resolve().parent/"evidence"
target.mkdir(exist_ok=True)
patterns=["shipped-*.txt","sweep-*.txt","short-a-*.txt","final-core-*.txt","final-short-*.txt",
          "empty-tail-*.txt","working-core-*.txt","working-short-*.txt",
          "working-shapes-*.txt","observed-check-*.txt","wide-recheck-*.txt",
          "storage-*.txt","proof-*.txt","layout-*.json","layout-*.txt",
          "remedy*-*.txt","accepted-*.txt","accepted-layout-*.json",
          "accepted-storage-*.txt","accepted-proof-*.txt",
          "profile-*-top.txt","profile-*.txt","source-*.json",
          "*-digest.json","*-probe.json","*-correctness.txt","metadata.json"]
for pattern in patterns:
    for path in source.glob(pattern):
        if path.is_file():shutil.copy2(path,target/path.name)
for path in source.glob("*-summary.json"):
    if path.stem in ("pilot-summary","screen-summary"): continue
    data=json.loads(path.read_text())
    by_variant={}
    for case,variants in data.items():
        for variant,metrics in variants.items():by_variant.setdefault(variant,{})[case]=metrics
    for variant,values in by_variant.items():
        items=list(values.items())
        for start in range(0,len(items),40):
            dest=target/f"{path.stem}-{variant}-{start//40+1}.json"
            dest.write_text(json.dumps(dict(items[start:start+40]),indent=2))
for path in target.iterdir():
    if path.is_file():assert len(path.read_bytes().splitlines())<=500,path
print(target)

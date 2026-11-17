#!/bin/sh
set -eu

fixtures=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
cd "$fixtures/../../.."
: "${RULEIX_PRODUCTION_DUMP:?Set RULEIX_PRODUCTION_DUMP to the production JSON dump path}"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

python3 - "$fixtures" "$scratch" <<'PY'
import json
from pathlib import Path
import shutil
import sys

fixtures, scratch = (Path(path) for path in sys.argv[1:])
overlays = {}
for source in fixtures.glob("*_test.go.txt"):
    filename = source.stem
    destination = scratch / filename
    shutil.copyfile(source, destination)
    overlays[str(Path.cwd() / filename)] = str(destination)
(scratch / "probe.json").write_text(json.dumps({"Replace": overlays}))
PY

export GOMAXPROCS=2
export RULEIX_COMBINATION_OUTPUT="$scratch/census.jsonl"
for size in 64 512; do
    export RULEIX_COMBINATION_REQUESTS="$size"
    go test -overlay "$scratch/probe.json" -run '^TestProductionCombinationOpportunity$' -count=1 -v .
    for model in summary structural sparse trie; do
        python3 "$fixtures/$model.py" "$RULEIX_COMBINATION_OUTPUT"
    done
done

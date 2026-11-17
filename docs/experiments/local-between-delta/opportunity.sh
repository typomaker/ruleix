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
sources = {
    "combination_internal_test.go": fixtures.parent / "local-combination-table" / "combination_internal_test.go.txt",
    "between_delta_internal_test.go": fixtures / "probe_internal_test.go.txt",
    "between_delta_dump_test.go": fixtures / "probe_dump_test.go.txt",
}
overlays = {}
for name, source in sources.items():
    destination = scratch / name
    shutil.copyfile(source, destination)
    overlays[str(Path.cwd() / name)] = str(destination)
(scratch / "opportunity.json").write_text(json.dumps({"Replace": overlays}))
PY

export GOMAXPROCS=2
export RULEIX_COMBINATION_REQUESTS=512
export RULEIX_COMBINATION_OUTPUT="$scratch/census.jsonl"
go test -overlay "$scratch/opportunity.json" -run '^TestProductionBetweenDeltaOpportunity$' -count=1 -v .
python3 "$fixtures/summarize_opportunity.py" "$scratch/census.jsonl"

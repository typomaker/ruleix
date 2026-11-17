#!/bin/sh
set -eu

fixtures=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
cd "$fixtures/../../.."
: "${RULEIX_PRODUCTION_DUMP:?Set RULEIX_PRODUCTION_DUMP to the production JSON dump path}"
scratch=$(mktemp -d)
printf 'Retaining experiment output in %s\n' "$scratch"
python3 "$fixtures/prepare.py" "$scratch"

versions="base candidate reuse cursor separate separate-cursor"
for version in $versions; do
    GOMAXPROCS=2 go test -overlay "$scratch/$version.json" -c -o "$scratch/$version.test" .
done

export GOMAXPROCS=1
for version in $versions; do
    "$scratch/$version.test" -test.run '^(TestBetweenDeltaMaskDifferential|TestProductionDumpLocalMatchesIndex)$' -test.v
done

for version in base separate separate-cursor separate-cursor separate base base separate separate-cursor; do
    for mode in mixed broad rotate narrow; do
        printf '%s %s\n' "$version" "$mode"
        case "$mode" in
            mixed) RULEIX_PLANNER_BUCKET='' RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem ;;
            rotate) RULEIX_PLANNER_BUCKET='' RULEIX_PLANNER_ROTATE=1 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem ;;
            *) RULEIX_PLANNER_BUCKET="$mode" RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem ;;
        esac
    done
    printf 'short %s\n' "$version"
    "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkBetweenDeltaShortRequests$' -test.benchtime=60000x -test.benchmem
done

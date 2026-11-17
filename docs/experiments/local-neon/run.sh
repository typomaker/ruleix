#!/bin/sh
set -eu
fixtures=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
cd "$fixtures/../../.."
: "${RULEIX_PRODUCTION_DUMP:?Set RULEIX_PRODUCTION_DUMP to the production JSON dump path}"
scratch=$(mktemp -d)
printf 'Retaining experiment output in %s\n' "$scratch"
python3 "$fixtures/prepare.py" "$scratch"

for version in base pack-go pack-neon fused cached selective selective-cached selective-fused fused-value selective-fused-value selected; do
    GOMAXPROCS=2 go test -overlay "$scratch/$version.json" -c -o "$scratch/$version.test" .
done
export GOMAXPROCS=1
for version in base pack-go pack-neon fused cached selective selective-cached selective-fused fused-value selective-fused-value selected; do
    "$scratch/$version.test" -test.run '^(TestNEON|TestPackedBetweenCache|TestProductionDumpLocalMatchesIndex)' -test.v
done
for version in base selected selected base base selected; do
    for mode in mixed tiny medium broad rotate; do
        printf '%s %s\n' "$version" "$mode"
        case "$mode" in
            mixed) RULEIX_PLANNER_BUCKET='' RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=100000x -test.benchmem ;;
            rotate) RULEIX_PLANNER_BUCKET='' RULEIX_PLANNER_ROTATE=1 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=100000x -test.benchmem ;;
            *) RULEIX_PLANNER_BUCKET="$mode" RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=100000x -test.benchmem ;;
        esac
    done
done

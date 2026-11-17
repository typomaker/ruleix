#!/bin/sh
set -eu
fixtures=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
: "${RULEIX_PRODUCTION_DUMP:?Set RULEIX_PRODUCTION_DUMP to the production JSON dump path}"
scratch=$(python3 "$fixtures/prepare.py")
printf 'Retaining comparison files in %s\n' "$scratch"
cat "$scratch/revisions.txt"
variants=${RULEIX_EMPTY_VARIANTS:-'base accepted'}
sequence=${RULEIX_EMPTY_SEQUENCE:-'base accepted accepted base base accepted'}
iterations=${RULEIX_EMPTY_ITERATIONS:-80000x}
for variant in $variants; do
    (
        cd "$scratch/source"
        GOMAXPROCS=2 go test -overlay="$scratch/$variant.json" -c -o "$scratch/$variant.test" .
    )
    "$scratch/$variant.test" -test.list EmptyMatrix | rg -q '^BenchmarkEmptyMatrix$'
    if test "$variant" != base && test -f "$scratch/base.test" && cmp -s "$scratch/base.test" "$scratch/$variant.test"; then
        printf 'Ignored overlay: %s is byte-identical to baseline\n' "$variant" >&2
        exit 1
    fi
done
export GOMAXPROCS=1
for variant in $variants; do
    RULEIX_COMPARISON_SMALL=1 "$scratch/$variant.test" -test.run '^(TestProductionEmptyControls|TestProductionVersionDigest|TestProductionDumpLocalMatchesIndex|TestExperimentLayout)$' -test.v
done
for variant in $sequence; do
    printf 'variant %s\n' "$variant"
    "$scratch/$variant.test" -test.run '^$' -test.bench '^BenchmarkEmpty(Matrix|Short|PublicControls)$' -test.benchtime="$iterations" -test.benchmem
done
for variant in base accepted accepted base; do
    case " $variants " in *" $variant "*) ;; *) continue ;; esac
    printf 'storage %s\n' "$variant"
    "$scratch/$variant.test" -test.run '^$' -test.bench '^BenchmarkProductionDump(Build|Retained)Probe$' -test.benchtime=10x -test.benchmem
    "$scratch/$variant.test" -test.run '^$' -test.bench '^BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe|ReopenedLocalRetainedProbe)$' -test.benchtime=1000x -test.benchmem
 done

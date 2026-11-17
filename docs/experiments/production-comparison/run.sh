#!/bin/sh
set -eu

fixtures=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
cd "$fixtures/../../.."
: "${RULEIX_PRODUCTION_DUMP:?Set RULEIX_PRODUCTION_DUMP to the production JSON dump path}"
scratch=$(mktemp -d)
printf 'Retaining comparison files in %s\n' "$scratch"

for version in v082 current; do
    case "$version" in
        v082) revision=${RULEIX_BASELINE_REVISION:-v0.8.2} ;;
        current) revision=${RULEIX_CURRENT_REVISION:-086a91b} ;;
    esac
    mkdir "$scratch/$version"
    git archive "$revision" | tar -x -C "$scratch/$version"
    for name in production_dump_schema_test.go production_dump_workload_test.go production_dump_local_differential_test.go; do
        {
            printf '//go:build go1.24\n\n'
            git show "086a91b:$name"
        } > "$scratch/$version/$name"
    done
    if test "${RULEIX_COMPARISON_SMALL:-0}" = 1; then
        patch "$scratch/$version/production_dump_workload_test.go" < "$fixtures/small-cohorts.patch"
    fi
    cp "$fixtures/digest_test.go.txt" "$scratch/$version/production_comparison_digest_test.go"
    {
        printf '//go:build go1.24\n\n'
        git show 086a91b:docs/experiments/local-neon/probe_test.go.txt
    } > "$scratch/$version/production_comparison_probe_test.go"
    if test "$version" = v082; then
        go mod edit -modfile "$scratch/$version/go.mod" -require=github.com/google/uuid@v1.6.0
    fi
    (
        cd "$scratch/$version"
        GOMAXPROCS=2 go test -mod=mod -c -o "$scratch/$version.test" .
    )
done

export GOMAXPROCS=1
for version in v082 current; do
    RULEIX_COMPARISON_DIGEST="$scratch/$version-digest.json" "$scratch/$version.test" -test.run '^(TestProductionDumpLocalMatchesIndex|TestProductionVersionDigest)$' -test.v
done
cmp "$scratch/v082-digest.json" "$scratch/current-digest.json"

modes="mixed tiny medium broad rotate"
if test "${RULEIX_COMPARISON_SMALL:-0}" = 1; then
    modes="zero tiny"
fi
for version in v082 current current v082 v082 current; do
    for mode in $modes; do
        printf '%s %s\n' "$version" "$mode"
        case "$mode" in
            mixed) RULEIX_PLANNER_BUCKET='' RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=80000x -test.benchmem ;;
            rotate) RULEIX_PLANNER_BUCKET='' RULEIX_PLANNER_ROTATE=1 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=50000x -test.benchmem ;;
            zero|tiny)
                iterations=50000x
                if test "${RULEIX_COMPARISON_SMALL:-0}" = 1; then iterations=80000x; fi
                RULEIX_PLANNER_BUCKET="$mode" RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime="$iterations" -test.benchmem ;;
            *) RULEIX_PLANNER_BUCKET="$mode" RULEIX_PLANNER_ROTATE=0 "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDumpLocalRequest100$' -test.benchtime=50000x -test.benchmem ;;
        esac
    done
done

for version in v082 current current v082; do
    printf 'storage %s\n' "$version"
    "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkSIMDShortRequests$' -test.benchtime=50000x -test.benchmem
    "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDump(Build|Retained)Probe$' -test.benchtime=10x -test.benchmem
    "$scratch/$version.test" -test.run '^$' -test.bench '^BenchmarkProductionDump(FreshLocal100|LocalRetainedProbe|ReopenedLocalRetainedProbe)$' -test.benchtime=1000x -test.benchmem
done

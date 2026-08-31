package ruleix

import (
	"math"
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type codecFixtureConstraint[V comparable] struct {
	value   V
	present bool
}

type fixtureNamedBool bool
type fixtureNamedInt int64
type fixtureNamedUint uint64
type fixtureNamedFloat float64
type fixtureNamedString string
type fixtureNamedBytes [16]byte
type fixtureNamedStrings [2]string
type fixtureNamedInts [3]int
type fixtureUUID [16]byte
type fixtureStruct struct {
	flag bool
	code int32
	name string
}

// TestLossyCodecBaselineFixtures freezes the exact-first behavior before
// build-compiled codecs replace the universal fallback. Selective expectations
// intentionally describe only the current baseline, not the desired codec
// contract from ROADMAP.md.
func TestLossyCodecBaselineFixtures(t *testing.T) {
	testCodecFixture(t, "bool", []bool{false, true}, true)
	testCodecFixture(t, "named-bool", []fixtureNamedBool{false, true}, false)
	testCodecFixture(t, "int64", []int64{-2, -1, 0, 1, 2}, true)
	testCodecFixture(t, "named-int64", []fixtureNamedInt{-2, -1, 0, 1, 2}, false)
	testCodecFixture(t, "uint64", []uint64{0, 1, 2, math.MaxUint64}, true)
	testCodecFixture(t, "named-uint64", []fixtureNamedUint{0, 1, 2, math.MaxUint64}, false)
	testCodecFixture(t, "float64", []float64{-1.5, math.Copysign(0, -1), 0, 1.5}, true)
	testCodecFixture(t, "named-float64", []fixtureNamedFloat{-1.5, 0, 1.5}, false)
	testCodecFixture(t, "string", []string{"", "a", "ab", "b"}, true)
	testCodecFixture(t, "named-string", []fixtureNamedString{"", "a", "ab", "b"}, false)
	testCodecFixture(t, "bytes-16", [][16]byte{{1}, {15: 1}, {1, 2, 3}}, true)
	testCodecFixture(t, "named-bytes-16", []fixtureNamedBytes{{1}, {15: 1}, {1, 2, 3}}, false)
	testCodecFixture(t, "strings-2", [][2]string{{"a", "bc"}, {"ab", "c"}, {"", "x"}}, true)
	testCodecFixture(t, "named-strings-2", []fixtureNamedStrings{{"a", "bc"}, {"ab", "c"}}, false)
	testCodecFixture(t, "ints-3", [][3]int{{1, 2, 3}, {3, 2, 1}}, false)
	testCodecFixture(t, "named-ints-3", []fixtureNamedInts{{1, 2, 3}, {3, 2, 1}}, false)
	testCodecFixture(t, "comparable-struct", []fixtureStruct{{true, 1, "a"}, {false, 2, "b"}}, false)
	testCodecFixture(t, "named-uuid", []fixtureUUID{{1}, {15: 1}, {1, 2, 3}}, false)
	testCodecFixture(t, "google-uuid", []uuid.UUID{{1}, {15: 1}, {1, 2, 3}}, false)
}

func testCodecFixture[V comparable](t *testing.T, name string, values []V, wantSelective bool) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		constraints := make([]codecFixtureConstraint[V], 513)
		ids := make([]int, len(constraints))
		for i := range constraints {
			constraints[i] = codecFixtureConstraint[V]{value: values[i%len(values)], present: i%19 != 0}
			ids[i] = i
		}
		get := func(value codecFixtureConstraint[V]) (V, bool) { return value.value, value.present }
		exact := buildCodecFixtureIndex(t, constraints, ids, Include(get))

		state := Include(get).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[codecFixtureConstraint[V], V])
		for i, constraint := range constraints {
			state.insert(constraint, uint32(i))
		}
		ladder, err := state.newLossyAllPlanner().representationLadder()
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(ladder), 2)
		minimum := ladder[len(ladder)-1].details.MemoryUsageBytes

		var inspector Inspector
		lossy := buildCodecFixtureIndex(t, constraints, ids, Inspect(&inspector,
			Lossy(Include(get), MemoryLimit(minimum))))
		snapshot := inspector.Snapshot()
		require.Equal(t, RuleModeLossy, snapshot.Mode())
		retained, ok := snapshot.MemoryUsage()
		require.True(t, ok)
		require.LessOrEqual(t, retained, minimum)
		granularity, ok := snapshot.Granularity()
		require.True(t, ok)
		if wantSelective {
			require.Equal(t, "lossy-grouped-hash", snapshot.Strategy())
		} else {
			require.Equal(t, uint64(1), granularity)
			require.Equal(t, "lossy-equality", snapshot.Strategy())
		}

		var candidateTotal, queryCount int
		for _, query := range append(values, *new(V)) {
			for _, present := range []bool{true, false} {
				input := codecFixtureConstraint[V]{value: query, present: present}
				var want, got []int
				exact.Search(input, &want)
				lossy.Search(input, &got)
				requireSupersetComparable(t, want, got)
				candidateTotal += len(got)
				queryCount++
			}
		}

		local := lossy.Local()
		defer local.Close()
		query := codecFixtureConstraint[V]{value: values[0], present: true}
		matches := make([]int, 0, len(ids))
		local.Search(query, &matches)
		local.Search(query, &matches)
		allocs := testing.AllocsPerRun(100, func() {
			matches = matches[:0]
			local.Search(query, &matches)
		})
		if !codecFixtureRaceEnabled {
			require.Zero(t, allocs)
		}
		t.Logf("exact-accounted=%d retained=%d strategy=%s granularity=%d candidates/query=%.2f warm-allocs=%.0f",
			ladder[0].details.MemoryUsageBytes, retained, snapshot.Strategy(), granularity,
			float64(candidateTotal)/float64(queryCount), allocs)
	})
}

func buildCodecFixtureIndex[V comparable](
	t testing.TB,
	constraints []codecFixtureConstraint[V],
	ids []int,
	rule Rule[codecFixtureConstraint[V]],
) *Index[codecFixtureConstraint[V], int] {
	t.Helper()
	index, err := New[codecFixtureConstraint[V], int](rule).Build(Zip(constraints, ids))
	require.NoError(t, err)
	return index
}

func TestLossyCodecFixtureBuildOrderAndWorkingPressure(t *testing.T) {
	const entries = 32768
	constraints := make([]codecFixtureConstraint[fixtureUUID], entries)
	ids := make([]int, entries)
	for i := range constraints {
		constraints[i] = codecFixtureConstraint[fixtureUUID]{value: fixtureUUID{byte(i), byte(i >> 8), byte(i >> 16)}, present: true}
		ids[i] = i
	}
	get := func(value codecFixtureConstraint[fixtureUUID]) (fixtureUUID, bool) { return value.value, value.present }

	exactUsage := codecFixtureUsage(t, constraints, ids, get, math.MaxUint64)
	state := Include(get).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[codecFixtureConstraint[fixtureUUID], fixtureUUID])
	for i, constraint := range constraints {
		state.insert(constraint, uint32(i))
	}
	ladder, err := state.newLossyAllPlanner().representationLadder()
	require.NoError(t, err)
	limit := ladder[len(ladder)-1].details.MemoryUsageBytes
	require.Greater(t, exactUsage, limit+limit/5, "exact-first working state must materially exceed the future soft target")
	t.Logf("exact-first-accounted-working=%d retained-limit=%d future-soft-target=%d checkpoints=%d",
		exactUsage, limit, limit+limit/5, entries/4096)

	baseline := codecFixtureSnapshot(t, constraints, ids, get, limit)
	repeated := codecFixtureSnapshot(t, constraints, ids, get, limit)
	require.Equal(t, baseline, repeated)

	permutation := rand.New(rand.NewSource(1)).Perm(entries) // fixed seed: schema-order fixture, not fuzzing.
	shuffledConstraints := make([]codecFixtureConstraint[fixtureUUID], entries)
	shuffledIDs := make([]int, entries)
	for i, source := range permutation {
		shuffledConstraints[i], shuffledIDs[i] = constraints[source], ids[source]
	}
	shuffled := codecFixtureSnapshot(t, shuffledConstraints, shuffledIDs, get, limit)
	require.Equal(t, baseline, shuffled)
}

type codecFixturePlanSnapshot struct {
	mode        RuleMode
	strategy    string
	usage       uint64
	granularity uint64
}

func codecFixtureSnapshot[V comparable](t testing.TB, constraints []codecFixtureConstraint[V], ids []int,
	get Getter[codecFixtureConstraint[V], V], limit uint64,
) codecFixturePlanSnapshot {
	t.Helper()
	var inspector Inspector
	buildCodecFixtureIndex(t, constraints, ids, Inspect(&inspector, Lossy(Include(get), MemoryLimit(limit))))
	snapshot := inspector.Snapshot()
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	granularity, _ := snapshot.Granularity()
	return codecFixturePlanSnapshot{snapshot.Mode(), snapshot.Strategy(), usage, granularity}
}

func codecFixtureUsage[V comparable](t testing.TB, constraints []codecFixtureConstraint[V], ids []int,
	get Getter[codecFixtureConstraint[V], V], limit uint64,
) uint64 {
	t.Helper()
	return codecFixtureSnapshot(t, constraints, ids, get, limit).usage
}

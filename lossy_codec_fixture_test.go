package ruleix

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"testing"
	"time"

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
type fixtureInterfaceStruct struct{ value any }

type fixtureUUIDAggregateConstraint struct {
	values [16]fixtureUUID
}

func TestLossyCodecScalarFixtures(t *testing.T) {
	testCodecFixture(t, "bool", []bool{false, true}, true)
	testCodecFixture(t, "named-bool", []fixtureNamedBool{false, true}, true)
	testCodecFixture(t, "int64", []int64{-2, -1, 0, 1, 2}, true)
	testCodecFixture(t, "named-int64", []fixtureNamedInt{-2, -1, 0, 1, 2}, true)
	testCodecFixture(t, "uint64", []uint64{0, 1, 2, math.MaxUint64}, true)
	testCodecFixture(t, "named-uint64", []fixtureNamedUint{0, 1, 2, math.MaxUint64}, true)
	testCodecFixture(t, "float64", []float64{-1.5, math.Copysign(0, -1), 0, 1.5}, true)
	testCodecFixture(t, "named-float64", []fixtureNamedFloat{-1.5, 0, 1.5}, true)
	testCodecFixture(t, "string", []string{"", "a", "ab", "b"}, true)
	testCodecFixture(t, "named-string", []fixtureNamedString{"", "a", "ab", "b"}, true)
	testCodecFixture(t, "bytes-16", [][16]byte{{1}, {15: 1}, {1, 2, 3}}, true)
	testCodecFixture(t, "strings-2", [][2]string{{"a", "bc"}, {"ab", "c"}, {"", "x"}}, true)
	testCodecFixture(t, "named-bytes-16", []fixtureNamedBytes{{1}, {15: 1}, {1, 2, 3}}, true)
	testCodecFixture(t, "named-strings-2", []fixtureNamedStrings{{"a", "bc"}, {"ab", "c"}}, true)
	testCodecFixture(t, "ints-3", [][3]int{{1}, {0, 1}, {0, 0, 1}}, true)
	testCodecFixture(t, "named-ints-3", []fixtureNamedInts{{1}, {0, 1}, {0, 0, 1}}, true)
	testCodecFixture(t, "comparable-struct", []fixtureStruct{{flag: true}, {code: 1}, {name: "x"}}, true)
	testCodecFixture(t, "named-uuid", []fixtureUUID{{1}, {15: 1}, {1, 2, 3}}, true)
	testCodecFixture(t, "google-uuid", []uuid.UUID{{1}, {15: 1}, {1, 2, 3}}, true)
	testCodecFixture(t, "time", []time.Time{time.Unix(0, 0), time.Unix(1, 2), time.Unix(1, 2).In(time.FixedZone("x", 60))}, true)
}

func TestLossyCodecUnsupportedCompositeErrors(t *testing.T) {
	testUnsupportedCodecFixture(t, "interface-field", fixtureInterfaceStruct{})
}

func TestEqualityCodecUUIDHashesEveryByte(t *testing.T) {
	codec, err := compileEqualityCodec[fixtureUUID]()
	require.NoError(t, err)
	zero := fixtureUUID{}
	for index := range len(zero) {
		changed := zero
		changed[index] = 1
		require.NotEqual(t, codec.hash(zero), codec.hash(changed), "byte %d was not mixed", index)
	}
}

func TestEqualityCodecUUIDCollisionDistribution(t *testing.T) {
	ordinary, err := compileEqualityCodec[[16]byte]()
	require.NoError(t, err)
	named, err := compileEqualityCodec[fixtureUUID]()
	require.NoError(t, err)
	ordinaryBuckets, namedBuckets := make(map[uint16]struct{}), make(map[uint16]struct{})
	for value := range uint64(10_000) {
		var ordinaryValue [16]byte
		binary.BigEndian.PutUint64(ordinaryValue[8:], value)
		namedValue := fixtureUUID(ordinaryValue)
		ordinaryBuckets[uint16(ordinary.hash(ordinaryValue)>>48)] = struct{}{}
		namedBuckets[uint16(named.hash(namedValue)>>48)] = struct{}{}
	}
	require.Greater(t, len(ordinaryBuckets), 8_000)
	require.Greater(t, len(namedBuckets), 8_000)
	delta := len(ordinaryBuckets) - len(namedBuckets)
	if delta < 0 {
		delta = -delta
	}
	require.Less(t, delta, 500)
}

func TestEqualityCodecIntegerCollisionDistribution(t *testing.T) {
	ordinary, err := compileEqualityCodec[int64]()
	require.NoError(t, err)
	named, err := compileEqualityCodec[fixtureNamedInt]()
	require.NoError(t, err)
	ordinaryBuckets, namedBuckets := make(map[uint16]struct{}), make(map[uint16]struct{})
	for value := range int64(10_000) {
		ordinaryBuckets[uint16(ordinary.hash(value)>>48)] = struct{}{}
		namedBuckets[uint16(named.hash(fixtureNamedInt(value))>>48)] = struct{}{}
	}
	require.Greater(t, len(ordinaryBuckets), 8_000)
	require.Greater(t, len(namedBuckets), 8_000)
}

func TestEqualityBucketCountLadder(t *testing.T) {
	require.Equal(t, []uint64{65536, 57344, 49152, 40960, 32768}, equalityBucketCounts(16)[:5])
	require.Equal(t, uint64(1), equalityBucketCounts(16)[len(equalityBucketCounts(16))-1])

	// Nested reduction covers exactly [0, bucketCount) for the extreme hashes.
	for _, bucketCount := range []uint64{1, 5, 40960, 57344, 65536} {
		require.Zero(t, reduceEqualityHash(0, bucketCount))
		require.Equal(t, bucketCount-1, reduceEqualityHash(math.MaxUint64, bucketCount))
	}
}

func TestEqualityBucketLadderIsNested(t *testing.T) {
	counts := equalityBucketCounts(8)
	for i := 0; i < len(counts)-1; i++ {
		current, next := counts[i], counts[i+1]
		parents := make(map[uint64]uint64, current)
		for hashBucket := uint64(0); hashBucket < 256; hashBucket++ {
			hash := hashBucket << 56
			currentKey := reduceEqualityHash(hash, current)
			nextKey := reduceEqualityHash(hash, next)
			if parent, ok := parents[currentKey]; ok {
				require.Equal(t, parent, nextKey)
			} else {
				parents[currentKey] = nextKey
			}
			require.Equal(t, nextKey, coarsenEqualityBucket(currentKey, current, next))
		}
	}
}

func TestLossyUUIDUsesFinerBucketCounts(t *testing.T) {
	const entries = 10_000
	state := Include(func(value codecFixtureConstraint[fixtureUUID]) (fixtureUUID, bool) {
		return value.value, value.present
	}).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[codecFixtureConstraint[fixtureUUID], fixtureUUID])
	for i := range entries {
		var value fixtureUUID
		binary.BigEndian.PutUint64(value[8:], uint64(i))
		state.insert(codecFixtureConstraint[fixtureUUID]{value: value, present: true}, uint32(i))
	}
	ladder, err := state.newLossyAllPlanner().representationLadder()
	require.NoError(t, err)

	counts := make([]uint64, 0, len(ladder)-1)
	for _, level := range ladder[1:] {
		detailed := level.compiled.(*inspectionDetailsRule[codecFixtureConstraint[fixtureUUID]])
		counts = append(counts, detailed.child.(*quantizedEqualityRule[codecFixtureConstraint[fixtureUUID], fixtureUUID]).quantizer.bucketCount)
	}
	for _, want := range []uint64{65536, 57344, 49152, 40960, 32768} {
		require.Contains(t, counts, want)
	}
}

func TestLossyAllUUIDTakesConsecutiveFinerDowngrades(t *testing.T) {
	const entries = 10_000
	constraints := make([]fixtureUUIDAggregateConstraint, entries)
	ids := make([]int, entries)
	for row := range constraints {
		ids[row] = row
		for column := range constraints[row].values {
			value := uint64(row)
			if column != 0 {
				value = uint64(row % 2)
			}
			binary.BigEndian.PutUint64(constraints[row].values[column][8:], value)
		}
	}

	makeRules := func(inspectors *[16]Inspector) Rule[fixtureUUIDAggregateConstraint] {
		children := make([]Rule[fixtureUUIDAggregateConstraint], len(inspectors))
		for column := range children {
			column := column
			get := func(value fixtureUUIDAggregateConstraint) (fixtureUUID, bool) {
				return value.values[column], true
			}
			child := Rule[fixtureUUIDAggregateConstraint](Include(get))
			if inspectors != nil {
				child = Inspect(&inspectors[column], child)
			}
			children[column] = child
		}
		return All(children...)
	}

	state := makeRules(nil).newState(&nodeIDAllocator{}, &buildStatistics{})
	for row, constraint := range constraints {
		state.insert(constraint, uint32(row))
	}
	leaves := []lossyAllLeaf[fixtureUUIDAggregateConstraint]{}
	require.NoError(t, collectLossyAllLeaves(state, &leaves))
	require.Len(t, leaves, 16)

	var total uint64
	for _, leaf := range leaves {
		total += leaf.exact
	}
	for step := 0; step < 3; step++ {
		best := selectLossyAllDowngrade(leaves)
		require.Equal(t, 0, best, "downgrade step %d must keep the 15 small UUID leaves exact", step+1)
		current := leaves[best].ladder[leaves[best].selected].details.MemoryUsageBytes
		leaves[best].selected++
		next := leaves[best].ladder[leaves[best].selected].details.MemoryUsageBytes
		total -= current - next
	}
	exact, err := New[fixtureUUIDAggregateConstraint, int](makeRules(nil)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var aggregate Inspector
	var inspectors [16]Inspector
	approximate, err := New[fixtureUUIDAggregateConstraint, int](
		Inspect(&aggregate, Lossy(makeRules(&inspectors), MemoryLimit(total))),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)
	usage, ok := aggregate.Snapshot().MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, total)
	require.Equal(t, RuleModeLossy, inspectors[0].Snapshot().Mode())
	granularity, ok := inspectors[0].Snapshot().Granularity()
	require.True(t, ok)
	// Streaming selects precision from the observed prefix and may therefore
	// retain a different (including finer) level than exact-first planning. The
	// hard aggregate limit and deterministic repeated build are the contract.
	require.Positive(t, granularity)
	for column := 1; column < len(inspectors); column++ {
		require.Equal(t, RuleModeExact, inspectors[column].Snapshot().Mode(), "leaf %d", column)
	}
	var repeatedInspectors [16]Inspector
	_, err = New[fixtureUUIDAggregateConstraint, int](
		Lossy(makeRules(&repeatedInspectors), MemoryLimit(total)),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for column := range inspectors {
		require.Equal(t, inspectors[column].Snapshot(), repeatedInspectors[column].Snapshot())
	}

	for row := 0; row < entries; row += 997 {
		var want, got []int
		exact.Search(constraints[row], &want)
		approximate.Search(constraints[row], &got)
		requireSupersetComparable(t, want, got)
	}
}

func testUnsupportedCodecFixture[V comparable](t *testing.T, name string, value V) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		get := func(constraint codecFixtureConstraint[V]) (V, bool) { return constraint.value, true }
		_, err := New[codecFixtureConstraint[V], int](
			Lossy(Include(get), MemoryLimit(1024)),
		).Build(Zip([]codecFixtureConstraint[V]{{value: value}}, []int{1}))
		var codecErr *equalityCodecError
		require.Error(t, err)
		require.True(t, errors.As(err, &codecErr))
	})
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
	constraints := make([]codecFixtureConstraint[fixtureNamedInt], entries)
	ids := make([]int, entries)
	for i := range constraints {
		constraints[i] = codecFixtureConstraint[fixtureNamedInt]{value: fixtureNamedInt(i), present: true}
		ids[i] = i
	}
	get := func(value codecFixtureConstraint[fixtureNamedInt]) (fixtureNamedInt, bool) {
		return value.value, value.present
	}

	exactUsage := codecFixtureUsage(t, constraints, ids, get, math.MaxUint64)
	state := Include(get).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[codecFixtureConstraint[fixtureNamedInt], fixtureNamedInt])
	for i, constraint := range constraints {
		state.insert(constraint, uint32(i))
	}
	ladder, err := state.newLossyAllPlanner().representationLadder()
	require.NoError(t, err)
	limit := ladder[len(ladder)-1].details.MemoryUsageBytes
	require.Greater(t, exactUsage, limit+limit/5, "exact-first working state must materially exceed the experimental soft target")
	t.Logf("exact-first-accounted-working=%d retained-limit=%d experimental-soft-target=%d checkpoints=%d",
		exactUsage, limit, limit+limit/5, entries/4096)

	baseline := codecFixtureSnapshot(t, constraints, ids, get, limit)
	repeated := codecFixtureSnapshot(t, constraints, ids, get, limit)
	require.Equal(t, baseline, repeated)

	permutation := rand.New(rand.NewSource(1)).Perm(entries) // fixed seed: schema-order fixture, not fuzzing.
	shuffledConstraints := make([]codecFixtureConstraint[fixtureNamedInt], entries)
	shuffledIDs := make([]int, entries)
	for i, source := range permutation {
		shuffledConstraints[i], shuffledIDs[i] = constraints[source], ids[source]
	}
	shuffled := codecFixtureSnapshot(t, shuffledConstraints, shuffledIDs, get, limit)
	require.Equal(t, baseline, shuffled)
	require.Equal(t, "lossy-grouped-hash", baseline.strategy)
	require.LessOrEqual(t, baseline.usage, limit)
}

func TestLossyBuildTargetSaturates(t *testing.T) {
	require.Equal(t, uint64(125), lossyBuildTarget(100))
	require.Equal(t, uint64(math.MaxUint64), lossyBuildTarget(math.MaxUint64))
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

var codecFixtureBenchmarkResult []int

// BenchmarkLossyCompiledScalarCodec compares direct and build-compiled scalar paths.
// 500ms x5 after scalar avalanche mixing: Int64 45.47-46.64 ns/op,
// NamedInt64 45.37-46.47 ns/op; both 0 B/op and 0 allocs/op. The pre-fix
// raw-FNV baselines were 188.8-190.1 and 189.7-190.9 ns/op respectively.
// Reproduce: go test -run '^$' -bench '^BenchmarkLossyCompiledScalarCodec$'
// -benchmem -benchtime=500ms -count=5 .
func BenchmarkLossyCompiledScalarCodec(b *testing.B) {
	benchmarkLossyCompiledScalarCodec(b, "Int64", func(value int) int64 { return int64(value) })
	benchmarkLossyCompiledScalarCodec(b, "NamedInt64", func(value int) fixtureNamedInt {
		return fixtureNamedInt(value)
	})
}

// BenchmarkLossyCompiledCompositeCodec measures the fixed-byte and recursive
// codec search paths with 10,000 entries and MemoryLimit(200000).
// Latest focused run (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 300ms x5):
// Bytes16 49.84-50.69 ns/op and NamedUUID 57.57-59.01 ns/op; both reported
// 0 B/op and 0 allocs/op. Other cases retain the broader benchmark coverage.
// Reproduce: go test -run '^$' -bench '^BenchmarkLossyCompiledCompositeCodec$'
// -benchmem -benchtime=300ms -count=3 .
func BenchmarkLossyCompiledCompositeCodec(b *testing.B) {
	benchmarkLossyCompiledScalarCodec(b, "Bytes16", func(value int) [16]byte {
		var result [16]byte
		binary.BigEndian.PutUint64(result[8:], uint64(value))
		return result
	})
	benchmarkLossyCompiledScalarCodec(b, "NamedUUID", func(value int) fixtureUUID {
		var result fixtureUUID
		binary.BigEndian.PutUint64(result[8:], uint64(value))
		return result
	})
	benchmarkLossyCompiledScalarCodec(b, "String", func(value int) string {
		return string(rune(value + 1))
	})
	benchmarkLossyCompiledScalarCodec(b, "IntArray", func(value int) fixtureNamedInts {
		return fixtureNamedInts{value, value + 1, value + 2}
	})
	benchmarkLossyCompiledScalarCodec(b, "Struct", func(value int) fixtureStruct {
		return fixtureStruct{flag: value&1 != 0, code: int32(value), name: string(rune(value + 1))}
	})
}

func benchmarkLossyCompiledScalarCodec[V comparable](b *testing.B, name string, value func(int) V) {
	b.Helper()
	b.Run(name, func(b *testing.B) {
		const entries = 10_000
		constraints := make([]codecFixtureConstraint[V], entries)
		ids := make([]int, entries)
		for i := range constraints {
			constraints[i] = codecFixtureConstraint[V]{value: value(i), present: true}
			ids[i] = i
		}
		get := func(constraint codecFixtureConstraint[V]) (V, bool) {
			return constraint.value, constraint.present
		}
		index, err := New[codecFixtureConstraint[V], int](
			Lossy(Include(get), MemoryLimit(200_000)),
		).Build(Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		local := index.Local()
		defer local.Close()
		query := codecFixtureConstraint[V]{value: value(entries / 2), present: true}
		result := make([]int, 0, 8)
		local.Search(query, &result)
		local.Search(query, &result)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			result = result[:0]
			local.Search(query, &result)
		}
		codecFixtureBenchmarkResult = result
	})
}

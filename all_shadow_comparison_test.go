package ruleix

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type allComparisonConstraint struct {
	segment, platform, cohort, excluded *int
	minimum, from, until, compared      *int
	operator                            *Operator
}

func allComparisonGetter(field func(allComparisonConstraint) *int) Getter[allComparisonConstraint, int] {
	return GetterFromPointer(field)
}

func allComparisonSchema(lossy bool, inspector *Inspector) Rule[allComparisonConstraint] {
	segment := Include(allComparisonGetter(
		func(value allComparisonConstraint) *int { return value.segment },
	))
	if lossy {
		segment = Inspect(inspector, Lossy(segment, MemoryLimit(1<<10)))
	}
	var nestedInspector Inspector
	return All(
		segment,
		Inspect(&nestedInspector, All(
			Include(allComparisonGetter(func(value allComparisonConstraint) *int { return value.platform })),
			Include(allComparisonGetter(func(value allComparisonConstraint) *int { return value.cohort })),
			GreaterOrEqual(
				allComparisonGetter(func(value allComparisonConstraint) *int { return value.minimum }),
				cmp.Compare[int],
			),
		)),
		Between(
			allComparisonGetter(func(value allComparisonConstraint) *int { return value.from }),
			allComparisonGetter(func(value allComparisonConstraint) *int { return value.until }),
			cmp.Compare[int],
		),
		CompareBy(
			allComparisonGetter(func(value allComparisonConstraint) *int { return value.compared }),
			GetterFromPointer(func(value allComparisonConstraint) *Operator { return value.operator }),
			cmp.Compare[int],
		),
		Exclude(allComparisonGetter(func(value allComparisonConstraint) *int { return value.excluded })),
	)
}

func TestAllAdaptiveExecutorMatchesMaterializeAllOracle(t *testing.T) {
	constraints := make([]allComparisonConstraint, 320)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = allComparisonConstraint{
			segment:  pointerUnless(i, i%5 == 0),
			platform: pointerUnless(i%5, i%6 == 0),
			cohort:   pointerUnless(i%3, i%6 == 0),
			minimum:  pointerUnless(i%17, i%8 == 0),
			from:     pointerUnless(i%13, i%9 == 0),
			until:    pointerUnless(13+i%19, i%10 == 0),
			compared: pointerUnless(i%23, i%12 == 0),
			operator: operatorUnless(Operator(i%5), i%12 == 0),
			excluded: pointerUnless(i%4, i%11 == 0),
		}
		// Exercise the Build contract that combines non-consecutive constraints
		// under one external ID and returns it at most once.
		ids[i] = i % 173
	}

	for _, lossy := range []bool{false, true} {
		t.Run(fmt.Sprintf("lossy=%t", lossy), func(t *testing.T) {
			var inspector Inspector
			index, err := New[allComparisonConstraint, int](
				allComparisonSchema(lossy, &inspector),
			).Build(Zip(constraints, ids))
			require.NoError(t, err)
			if lossy {
				require.Equal(t, RuleModeLossy, inspector.Snapshot().Mode())
			}
			for queryNumber := range 512 {
				query := allComparisonConstraint{
					segment:  pointerUnless(queryNumber%400, queryNumber%13 == 0),
					platform: pointerUnless(queryNumber%7, queryNumber%11 == 0),
					cohort:   pointerUnless(queryNumber%3, queryNumber%11 == 0),
					minimum:  pointerUnless(queryNumber%21, queryNumber%17 == 0),
					from:     pointerUnless(queryNumber%15, queryNumber%19 == 0),
					until:    pointerUnless(8+queryNumber%23, queryNumber%23 == 0),
					compared: pointerUnless(queryNumber%27, queryNumber%31 == 0),
					excluded: pointerUnless(queryNumber%4, queryNumber%29 == 0),
				}

				want := materializeAllOracle(index, query)
				var got []int
				index.Search(query, &got)
				require.Equal(t, want, got, "query %d", queryNumber)

				local := index.Local()
				for attempt := range 2 {
					var localGot []int
					local.Search(query, &localGot)
					require.Equal(t, want, localGot, "local query %d attempt %d", queryNumber, attempt)
				}
				local.Close()
			}
		})
	}
}

func FuzzAllAdaptiveExecutorMatchesMaterializeAllOracle(f *testing.F) {
	constraints := make([]allComparisonConstraint, 384)
	ids := make([]int, len(constraints))
	for i := range constraints {
		missing := i%17 == 0
		constraints[i] = allComparisonConstraint{
			segment:  pointerUnless(i%31, i%5 == 0),
			platform: pointerUnless(i%7, i%6 == 0),
			cohort:   pointerUnless(i%4, i%9 == 0),
			minimum:  pointerUnless(i%29, i%8 == 0),
			from:     pointerUnless(i%19, i%10 == 0),
			until:    pointerUnless(7+i%23, i%11 == 0),
			compared: pointerUnless(i%37, missing),
			operator: operatorUnless(Operator(i%5), missing),
			excluded: pointerUnless(i%6, i%13 == 0),
		}
		ids[i] = i % 211
	}
	var exactInspector, lossyInspector Inspector
	index, err := New[allComparisonConstraint, int](
		allComparisonSchema(false, &exactInspector),
	).Build(Zip(constraints, ids))
	require.NoError(f, err)
	lossyIndex, err := New[allComparisonConstraint, int](
		allComparisonSchema(true, &lossyInspector),
	).Build(Zip(constraints, ids))
	require.NoError(f, err)

	for _, seed := range [][]byte{
		{0},
		{0xf3},
		{1, 2, 3, 4, 5, 6, 7, 8},
		{0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			data = []byte{0}
		}
		value := func(at, modulus int) *int {
			b := data[at%len(data)]
			return pointerUnless(int(b)%modulus, b&0x80 != 0)
		}
		query := allComparisonConstraint{
			segment:  value(0, 41),
			platform: value(1, 9),
			cohort:   value(2, 6),
			minimum:  value(3, 35),
			from:     value(4, 25),
			until:    value(5, 31),
			compared: value(6, 43),
			excluded: value(7, 8),
		}

		want := materializeAllOracle(index, query)
		var got []int
		index.Search(query, &got)
		require.Equal(t, want, got)

		local := index.Local()
		defer local.Close()
		for attempt := range 2 {
			got = got[:0]
			local.Search(query, &got)
			require.Equal(t, want, got, "warm Local attempt %d", attempt)
		}

		lossyWant := materializeAllOracle(lossyIndex, query)
		got = got[:0]
		lossyIndex.Search(query, &got)
		requireOrderedSubset(t, lossyWant, got)
	})
}

func requireOrderedSubset[T comparable](t *testing.T, want, got []T) {
	t.Helper()
	next := 0
	for _, value := range got {
		if next < len(want) && value == want[next] {
			next++
		}
	}
	require.Equal(t, len(want), next, "lossy execution omitted or reordered exact results")
}

func pointerUnless(value int, missing bool) *int {
	if missing {
		return nil
	}
	return &value
}

func operatorUnless(value Operator, missing bool) *Operator {
	if missing {
		return nil
	}
	return &value
}

// materializeAllOracle is the executor being replaced: recursively materialize
// every All child into a complete bitmap, then intersect in schema order.
func materializeAllOracle[C any, ID comparable](index *Index[C, ID], value C) []ID {
	pool := newBitmapPool()
	bits := pool.get()
	materializeRuleOracle(index.root, value, bits, pool)
	if len(index.exclusions) != 0 {
		excluded := pool.get()
		addExclusions(index.exclusions, value, excluded, pool)
		bits.AndNot(excluded)
		pool.put(excluded)
	}
	result := appendBitmapValues(bits, index.values, []ID(nil))
	pool.put(bits)
	return result
}

func materializeRuleOracle[T any](rule Rule[T], value T, dst *roaring.Bitmap, pool *bitmapPool) {
	switch typed := rule.(type) {
	case *allRule[T]:
		if len(typed.children) == 0 {
			return
		}
		materializeRuleOracle(typed.children[0], value, dst, pool)
		for _, child := range typed.children[1:] {
			bits := pool.get()
			materializeRuleOracle(child, value, bits, pool)
			dst.And(bits)
			pool.put(bits)
		}
	case *inspectedRuntimeRule[T]:
		materializeRuleOracle(typed.child, value, dst, pool)
	case *inspectionDetailsRule[T]:
		materializeRuleOracle(typed.child, value, dst, pool)
	case *lossyRule[T]:
		materializeRuleOracle(typed.child, value, dst, pool)
	default:
		rule.search(value, dst, pool)
	}
}

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
	minimum, from, until                *int
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
			excluded: pointerUnless(i%4, i%11 == 0),
		}
		// Exercise the Build contract that combines non-consecutive constraints
		// under one external ID and returns it at most once.
		ids[i] = i % 173
	}

	for _, lossy := range []bool{false, true} {
		t.Run(fmt.Sprintf("lossy=%t", lossy), func(t *testing.T) {
			var inspector Inspector
			index, err := New[allComparisonConstraint, int](allComparisonSchema(lossy, &inspector)).Build(Zip(constraints, ids))
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

func pointerUnless(value int, missing bool) *int {
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

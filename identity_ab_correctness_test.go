package ruleix

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type identityMatrixConstraint struct {
	values  [8]int
	present [8]bool
	until   int
	op      Operator
}

func identityMatrixEqualitySchema(children int, inspectors *[8]Inspector) Rule[identityMatrixConstraint] {
	rules := make([]Rule[identityMatrixConstraint], children)
	for child := range children {
		column := child
		rule := Include(func(value identityMatrixConstraint) (int, bool) {
			return value.values[column], value.present[column]
		})
		if inspectors != nil {
			rule = Inspect(&inspectors[child], rule)
		}
		rules[child] = rule
	}
	return All(rules...)
}

func identityMatrixEntries(children, classes, duplicatePercent int) ([]identityMatrixConstraint, []int) {
	constraints := make([]identityMatrixConstraint, 0, classes*3+2)
	ids := make([]int, 0, cap(constraints))
	duplicateChildren := children * duplicatePercent / 100
	for class := range classes {
		for copy := range 3 {
			var constraint identityMatrixConstraint
			for child := range children {
				constraint.present[child] = true
				constraint.values[child] = class*17 + child
				if child < duplicateChildren {
					constraint.present[child] = (class+copy)%5 != 0
					constraint.values[child] = class
				}
			}
			constraints = append(constraints, constraint)
			ids = append(ids, len(ids))
		}
	}
	// Repeated, non-consecutive IDs make result-order equality observable.
	constraints = append(constraints, constraints[0], constraints[len(constraints)-1])
	ids = append(ids, ids[len(ids)-1], ids[0])
	return constraints, ids
}

// TestIdentityABEqualityCorrectnessMatrix is the table-driven correctness gate
// for the identity experiment. It deliberately mixes concrete and wildcard
// postings and duplicate external IDs while varying both class-mask width and
// physical duplication.
func TestIdentityABEqualityCorrectnessMatrix(t *testing.T) {
	for _, children := range []int{2, 4, 8} {
		for _, classes := range []int{2, 64, 65} {
			for _, duplication := range []int{0, 50, 100} {
				name := fmt.Sprintf("Children%d/Classes%d/Duplication%d", children, classes, duplication)
				t.Run(name, func(t *testing.T) {
					constraints, ids := identityMatrixEntries(children, classes, duplication)
					baseline := buildIdentityABIndex(t, identityBaseline,
						identityMatrixEqualitySchema(children, nil), constraints, ids)
					integrated := buildIdentityABIndex(t, identityIntegrated,
						identityMatrixEqualitySchema(children, nil), constraints, ids)

					var concrete identityMatrixConstraint
					for child := range children {
						concrete.values[child] = 1
						concrete.present[child] = true
					}
					queries := []identityMatrixConstraint{{}, concrete, constraints[0], constraints[len(constraints)/2]}
					for queryIndex, query := range queries {
						var want, got []int
						baseline.index.Search(query, &want)
						integrated.index.Search(query, &got)
						require.Equal(t, want, got, "query %d, classes=%d, counters=%+v",
							queryIndex, integrated.index.root.(*allRule[identityMatrixConstraint]).equalityClassCount,
							*integrated.counters)
					}
					require.Zero(t, integrated.counters.linearEqualityDedupRuns)
					if duplication == 100 && classes > 2 {
						require.Positive(t, integrated.counters.skippedOperands+
							integrated.counters.containsChecks)
						baselineWork := baseline.counters.physicalInspectorSearches + baseline.counters.containsChecks
						integratedWork := integrated.counters.physicalInspectorSearches + integrated.counters.containsChecks
						require.LessOrEqual(t, integratedWork, baselineWork)
					}
				})
			}
		}
	}
}

func TestBitmapRangesDisjointAcceptsEmptyOperand(t *testing.T) {
	require.True(t, bitmapRangesDisjoint(roaring.New(), roaring.BitmapOf(1)))
	require.True(t, bitmapRangesDisjoint(roaring.BitmapOf(1), roaring.New()))
}

func TestIdentityABCanonicalRangeAliasesAndCoincidentOperations(t *testing.T) {
	entries := []identityMatrixConstraint{
		{values: [8]int{1}, present: [8]bool{true}, until: 9, op: OperatorGTE},
		{values: [8]int{3}, present: [8]bool{true}, until: 7, op: OperatorLTE},
		{values: [8]int{5}, present: [8]bool{true}, until: 5, op: OperatorEQ},
		{values: [8]int{7}, present: [8]bool{true}, until: 3, op: OperatorGT},
		{},
	}
	ids := []int{4, 3, 3, 2, 1}
	constructors := []struct {
		name string
		make func() Rule[identityMatrixConstraint]
	}{
		{"Ordered", func() Rule[identityMatrixConstraint] {
			return GreaterOrEqual(
				func(v identityMatrixConstraint) (int, bool) { return v.values[0], v.present[0] },
				cmp.Compare[int],
			)
		}},
		{"Between", func() Rule[identityMatrixConstraint] {
			return Between(
				func(v identityMatrixConstraint) (int, bool) { return v.values[0], v.present[0] },
				func(v identityMatrixConstraint) (int, bool) { return v.until, v.present[0] }, cmp.Compare[int])
		}},
		{"CompareBy", func() Rule[identityMatrixConstraint] {
			return CompareBy(
				func(v identityMatrixConstraint) (int, bool) { return v.values[0], v.present[0] },
				func(v identityMatrixConstraint) (Operator, bool) { return v.op, v.present[0] }, cmp.Compare[int])
		}},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			shared := constructor.make()
			var baselineInspectors, integratedInspectors [2]Inspector
			baseline := buildIdentityABIndex(t, identityBaseline,
				All(Inspect(&baselineInspectors[0], shared), Inspect(&baselineInspectors[1], shared)), entries, ids)
			integrated := buildIdentityABIndex(t, identityIntegrated,
				All(Inspect(&integratedInspectors[0], shared), Inspect(&integratedInspectors[1], shared)), entries, ids)
			require.Len(t, baseline.index.root.(*allRule[identityMatrixConstraint]).children, 2)
			require.Len(t, integrated.index.root.(*allRule[identityMatrixConstraint]).children, 1,
				"canonical aliases must compile to one operation")
			for _, query := range entries {
				var want, got []int
				baseline.index.Search(query, &want)
				integrated.index.Search(query, &got)
				require.Equal(t, want, got)
			}
			for i := range baselineInspectors {
				require.Equal(t, baselineInspectors[i].Snapshot(), integratedInspectors[i].Snapshot())
			}

			// Separately constructed rules can have coincident results, but their
			// opaque operation identities must remain independent.
			independent := buildIdentityABIndex(t, identityIntegrated,
				All(constructor.make(), constructor.make()), entries, ids)
			require.Len(t, independent.index.root.(*allRule[identityMatrixConstraint]).children, 2)
		})
	}
}

func FuzzIdentityABEqualityDifferential(f *testing.F) {
	f.Add(uint8(2), uint8(7), uint8(100), int64(1))
	f.Add(uint8(8), uint8(65), uint8(50), int64(19))
	f.Fuzz(func(t *testing.T, rawChildren, rawClasses, rawDuplication uint8, seed int64) {
		children := 2 + int(rawChildren)%7
		classes := 1 + int(rawClasses)%70
		duplication := []int{0, 50, 100}[int(rawDuplication)%3]
		constraints, ids := identityMatrixEntries(children, classes, duplication)
		baseline := buildIdentityABIndex(t, identityBaseline,
			identityMatrixEqualitySchema(children, nil), constraints, ids)
		integrated := buildIdentityABIndex(t, identityIntegrated,
			identityMatrixEqualitySchema(children, nil), constraints, ids)
		for i := range 12 {
			var query identityMatrixConstraint
			value := int(seed) + i*31
			for child := range children {
				query.values[child] = (value + child) % classes
				query.present[child] = (value+child)%4 != 0
			}
			var want, got []int
			baseline.index.Search(query, &want)
			integrated.index.Search(query, &got)
			require.Equal(t, want, got)
		}
	})
}

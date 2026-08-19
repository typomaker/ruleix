package ruleix

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

const equalitySpecializationEntries = 38_098

type equalitySpecializationConstraint struct {
	value bool
}

var (
	equalitySpecializationBitmapResult *roaring.Bitmap
	equalitySpecializationIndexResult  []uint32
	equalitySpecializationRuleResult   Rule[equalitySpecializationConstraint]
)

func buildEqualitySpecializationRule(values int) *eqRule[equalitySpecializationConstraint, bool] {
	rule := &eqRule[equalitySpecializationConstraint, bool]{
		get:      func(value equalitySpecializationConstraint) (bool, bool) { return value.value, true },
		wildcard: roaring.New(),
		values:   newEqualityIndex[bool](values),
	}
	for id := range equalitySpecializationEntries {
		rule.insert(equalitySpecializationConstraint{value: values == 2 && id&1 == 1}, uint32(id))
	}
	return rule
}

// BenchmarkEqualitySpecialization compares the former general equality rule
// with the unary/binary rules produced by the build optimizer. RootSearch
// isolates filter lookup and posting-list materialization; IndexSearch includes
// conversion of every matched internal ID into the public result slice.
func BenchmarkEqualitySpecialization(b *testing.B) {
	for _, values := range []int{1, 2} {
		b.Run(fmt.Sprintf("Values/%d", values), func(b *testing.B) {
			oldRule := buildEqualitySpecializationRule(values)
			newRule := oldRule.optimize(equalitySpecializationEntries)
			prepareRuleSearch[equalitySpecializationConstraint](oldRule)
			prepareRuleSearch[equalitySpecializationConstraint](newRule)
			query := equalitySpecializationConstraint{value: values == 2}

			for _, implementation := range []struct {
				name string
				rule Rule[equalitySpecializationConstraint]
			}{{"OldGeneral", oldRule}, {"NewSpecialized", newRule}} {
				b.Run(implementation.name+"/RootSearch", func(b *testing.B) {
					pool := newBitmapPool()
					dst := roaring.New()
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						dst.Clear()
						implementation.rule.search(query, dst, pool)
					}
					equalitySpecializationBitmapResult = dst
				})

				b.Run(implementation.name+"/IndexSearch", func(b *testing.B) {
					ids := make([]uint32, equalitySpecializationEntries)
					for id := range ids {
						ids[id] = uint32(id)
					}
					index := &Index[equalitySpecializationConstraint, uint32]{
						root: implementation.rule, values: ids, pool: newBitmapPool(), nodes: 1,
					}
					var result []uint32
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						index.Search(query, &result)
					}
					equalitySpecializationIndexResult = result
				})
			}
		})
	}
}

func BenchmarkEqualitySpecializationBuild(b *testing.B) {
	for _, values := range []int{1, 2} {
		for _, specialize := range []bool{false, true} {
			name := "OldGeneral"
			if specialize {
				name = "NewSpecialized"
			}
			b.Run(fmt.Sprintf("Values/%d/%s", values, name), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					rule := buildEqualitySpecializationRule(values)
					if specialize {
						equalitySpecializationRuleResult = rule.optimize(equalitySpecializationEntries)
					} else {
						equalitySpecializationRuleResult = rule
					}
				}
				runtime.KeepAlive(equalitySpecializationRuleResult)
			})
		}
	}
}

// BenchmarkEqualitySpecializationRetained reports the deterministic memory
// owned by the rule shape itself. Posting bitmap storage is shared between the
// two variants and deliberately excluded from the metric.
func BenchmarkEqualitySpecializationRetained(b *testing.B) {
	setSize := uint64(unsafe.Sizeof(equalitySet{}))
	for _, values := range []int{1, 2} {
		oldRule := buildEqualitySpecializationRule(values)
		newRule := oldRule.optimize(equalitySpecializationEntries)
		for _, implementation := range []struct {
			name     string
			rule     Rule[equalitySpecializationConstraint]
			retained uint64
		}{
			{"OldGeneral", oldRule, uint64(unsafe.Sizeof(*oldRule)) + uint64(cap(oldRule.values.sets))*setSize},
			{"NewSpecialized", newRule, specializedEqualityRuleSize(newRule)},
		} {
			b.Run(fmt.Sprintf("Values/%d/%s", values, implementation.name), func(b *testing.B) {
				b.ReportMetric(float64(implementation.retained), "retained-B/rule")
				for range b.N {
					runtime.KeepAlive(implementation.rule)
				}
			})
		}
	}
}

func specializedEqualityRuleSize(rule Rule[equalitySpecializationConstraint]) uint64 {
	switch typed := rule.(type) {
	case *unaryEqRule[equalitySpecializationConstraint, bool]:
		return uint64(unsafe.Sizeof(*typed))
	case *binaryEqRule[equalitySpecializationConstraint, bool]:
		return uint64(unsafe.Sizeof(*typed))
	default:
		panic("unexpected equality specialization")
	}
}

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
	value int
}

var (
	equalitySpecializationBitmapResult *roaring.Bitmap
	equalitySpecializationIndexResult  []uint32
	equalitySpecializationRuleResult   Rule[equalitySpecializationConstraint]
)

func buildEqualitySpecializationRule(values int) *eqRule[equalitySpecializationConstraint, int] {
	rule := &eqRule[equalitySpecializationConstraint, int]{
		get:      func(value equalitySpecializationConstraint) (int, bool) { return value.value, true },
		wildcard: roaring.New(),
		values:   newEqualityIndex[int](values),
	}
	for id := range equalitySpecializationEntries {
		rule.insert(equalitySpecializationConstraint{value: id % values}, uint32(id))
	}
	return rule
}

func useLegacyThreeValueMap(rule *eqRule[equalitySpecializationConstraint, int]) {
	rule.values.offsets = make(map[int]uint32, 3)
	for i := range 3 {
		rule.values.offsets[rule.values.keys[i]] = uint32(i)
	}
	rule.values.count = 0
}

// BenchmarkEqualitySpecialization compares the former general equality rule
// with the unary/binary/ternary rules produced by the build optimizer. The
// three-value OldGeneral case recreates the map promotion used before the
// ternary specialization. RootSearch
// isolates filter lookup and posting-list materialization; IndexSearch includes
// conversion of every matched internal ID into the public result slice.
// Apple M1 Max, Go 1.26, 300ms x5: ternary RootSearch 14.31 ns/op versus the
// legacy map's 18.94 ns/op; retained rule shape 200 B versus 264 B (map storage
// excluded from that deterministic metric).
func BenchmarkEqualitySpecialization(b *testing.B) {
	for _, values := range []int{1, 2, 3} {
		b.Run(fmt.Sprintf("Values/%d", values), func(b *testing.B) {
			oldRule := buildEqualitySpecializationRule(values)
			candidate := buildEqualitySpecializationRule(values)
			if values == 3 {
				useLegacyThreeValueMap(oldRule)
			}
			newRule := candidate.optimize(equalitySpecializationEntries)
			prepareRuleSearch[equalitySpecializationConstraint](oldRule)
			prepareRuleSearch[equalitySpecializationConstraint](newRule)
			query := equalitySpecializationConstraint{value: values - 1}

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
					pool := newBitmapPool()
					pool.observeRuntime = false
					index := &Index[equalitySpecializationConstraint, uint32]{
						root: implementation.rule, values: ids, pool: pool, nodes: 1,
					}
					var result []uint32
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						result = result[:0]
						index.Search(query, &result)
					}
					equalitySpecializationIndexResult = result
				})
			}
		})
	}
}

func BenchmarkEqualitySpecializationBuild(b *testing.B) {
	for _, values := range []int{1, 2, 3} {
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
	for _, values := range []int{1, 2, 3} {
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
	case *unaryEqRule[equalitySpecializationConstraint, int]:
		return uint64(unsafe.Sizeof(*typed))
	case *binaryEqRule[equalitySpecializationConstraint, int]:
		return uint64(unsafe.Sizeof(*typed))
	case *ternaryEqRule[equalitySpecializationConstraint, int]:
		return uint64(unsafe.Sizeof(*typed))
	default:
		panic("unexpected equality specialization")
	}
}

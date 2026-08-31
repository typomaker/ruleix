package ruleix

import (
	"fmt"
	"testing"
)

var fixedEqualityLookupResult int

// BenchmarkFixedEqualityLookupCutover isolates the lookup tradeoff used to
// choose the largest fixed equality specialization. Keep the matrix broad:
// comparable values can have materially different equality costs. Apple M1
// Max, Go 1.26, 300-400ms x5: unrolled string miss is 7.17 ns at N=4, remains
// within 1.5% of map around 8.7 ns at N=5, and regresses to 10.84 ns versus
// map's 8.62 ns at N=6.
func BenchmarkFixedEqualityLookupCutover(b *testing.B) {
	benchmarkFixedEqualityLookupType(b, "Int", func(i int) int { return i })
	benchmarkFixedEqualityLookupType(b, "String", func(i int) string {
		return fmt.Sprintf("value-%02d", i)
	})
	benchmarkFixedEqualityLookupType(b, "Array16", func(i int) [16]byte {
		var value [16]byte
		value[0], value[15] = byte(i), byte(i*31)
		return value
	})
}

func benchmarkFixedEqualityLookupType[V comparable](b *testing.B, name string, key func(int) V) {
	b.Helper()
	for size := 4; size <= 8; size++ {
		keys := make([]V, size)
		mapped := make(map[V]int, size)
		for i := range size {
			keys[i] = key(i)
			mapped[keys[i]] = i
		}
		queries := []struct {
			name  string
			value V
		}{
			{"First", keys[0]},
			{"Middle", keys[size/2]},
			{"Last", keys[size-1]},
			{"Miss", key(size + 1)},
		}
		for _, query := range queries {
			b.Run(fmt.Sprintf("%s/N%d/%s", name, size, query.name), func(b *testing.B) {
				b.Run("Map", func(b *testing.B) {
					result := 0
					for range b.N {
						result = mapped[query.value]
					}
					fixedEqualityLookupResult = result
				})
				b.Run("Linear", func(b *testing.B) {
					result := 0
					for range b.N {
						for i := range keys {
							if keys[i] == query.value {
								result = i
								break
							}
						}
					}
					fixedEqualityLookupResult = result
				})
				if size == 4 {
					fixed := [4]V{keys[0], keys[1], keys[2], keys[3]}
					b.Run("Unrolled", func(b *testing.B) {
						result := 0
						for range b.N {
							result = fixedEqualityLookup4(fixed, query.value)
						}
						fixedEqualityLookupResult = result
					})
				}
				if size == 8 {
					fixed := [8]V{keys[0], keys[1], keys[2], keys[3], keys[4], keys[5], keys[6], keys[7]}
					b.Run("Unrolled", func(b *testing.B) {
						result := 0
						for range b.N {
							result = fixedEqualityLookup8(fixed, query.value)
						}
						fixedEqualityLookupResult = result
					})
				}
				if size >= 5 && size <= 7 {
					fixed := [7]V{keys[0], keys[1], keys[2], keys[3], keys[4]}
					if size >= 6 {
						fixed[5] = keys[5]
					}
					if size == 7 {
						fixed[6] = keys[6]
					}
					b.Run("Unrolled", func(b *testing.B) {
						result := 0
						for range b.N {
							switch size {
							case 5:
								result = fixedEqualityLookup5(fixed, query.value)
							case 6:
								result = fixedEqualityLookup6(fixed, query.value)
							case 7:
								result = fixedEqualityLookup7(fixed, query.value)
							}
						}
						fixedEqualityLookupResult = result
					})
				}
			})
		}
	}
}

func fixedEqualityLookup5[V comparable](keys [7]V, value V) int {
	if value == keys[0] {
		return 0
	}
	if value == keys[1] {
		return 1
	}
	if value == keys[2] {
		return 2
	}
	if value == keys[3] {
		return 3
	}
	if value == keys[4] {
		return 4
	}
	return -1
}
func fixedEqualityLookup6[V comparable](keys [7]V, value V) int {
	if found := fixedEqualityLookup5(keys, value); found >= 0 {
		return found
	}
	if value == keys[5] {
		return 5
	}
	return -1
}
func fixedEqualityLookup7[V comparable](keys [7]V, value V) int {
	if found := fixedEqualityLookup6(keys, value); found >= 0 {
		return found
	}
	if value == keys[6] {
		return 6
	}
	return -1
}

func fixedEqualityLookup8[V comparable](keys [8]V, value V) int {
	if value == keys[0] {
		return 0
	}
	if value == keys[1] {
		return 1
	}
	if value == keys[2] {
		return 2
	}
	if value == keys[3] {
		return 3
	}
	if value == keys[4] {
		return 4
	}
	if value == keys[5] {
		return 5
	}
	if value == keys[6] {
		return 6
	}
	if value == keys[7] {
		return 7
	}
	return -1
}

func fixedEqualityLookup4[V comparable](keys [4]V, value V) int {
	if value == keys[0] {
		return 0
	}
	if value == keys[1] {
		return 1
	}
	if value == keys[2] {
		return 2
	}
	if value == keys[3] {
		return 3
	}
	return -1
}

func BenchmarkFixedEqualityStringMissBoundary(b *testing.B) {
	for _, size := range []int{5, 6} {
		keys := [7]string{}
		for i := range keys {
			keys[i] = fmt.Sprintf("value-%02d", i)
		}
		mapped := make(map[string]int, size)
		for i := range size {
			mapped[keys[i]] = i
		}
		query := fmt.Sprintf("value-%02d", size+1)
		b.Run(fmt.Sprintf("N%d/Map", size), func(b *testing.B) {
			result := 0
			for range b.N {
				result = mapped[query]
			}
			fixedEqualityLookupResult = result
		})
		if size == 5 {
			b.Run("N5/Unrolled", func(b *testing.B) {
				result := 0
				for range b.N {
					result = fixedEqualityLookup5(keys, query)
				}
				fixedEqualityLookupResult = result
			})
		} else {
			b.Run("N6/Unrolled", func(b *testing.B) {
				result := 0
				for range b.N {
					result = fixedEqualityLookup6(keys, query)
				}
				fixedEqualityLookupResult = result
			})
		}
	}
}

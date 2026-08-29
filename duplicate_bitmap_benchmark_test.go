package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/typomaker/ruleix"
)

type duplicateBitmapValue struct {
	values [8]int
}

// BenchmarkLocalDuplicateEquality is the end-to-end baseline for cached-bitmap
// identity experiments. Build-time equality-component deduplication already
// recognizes the pointer-interned postings, so an additional per-query identity
// scan must beat these cold and warm results rather than claim the removed And
// operations as a new benefit.
//
// Latest local medians (Apple M1 Max, Go 1.26.0), children 2/4/8: cold
// 1.436/2.319/4.066 us, 656/752/944 B, 5/7/11 allocs; warm
// 720.3/760.5/831.2 ns, 0 B, 0 allocs.
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkLocalDuplicateEquality/' \
//	  -benchmem -benchtime=500ms -count=3 .
func BenchmarkLocalDuplicateEquality(b *testing.B) {
	for _, children := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("Children%d", children), func(b *testing.B) {
			rules := make([]ruleix.Rule[duplicateBitmapValue], children)
			for child := range children {
				index := child
				rules[child] = ruleix.Include(func(value duplicateBitmapValue) (int, bool) {
					return value.values[index], true
				})
			}
			constraints := make([]duplicateBitmapValue, 4096)
			ids := make([]int, len(constraints))
			for id := range constraints {
				for child := range children {
					constraints[id].values[child] = id % 16
				}
				ids[id] = id
			}
			index, err := ruleix.New[duplicateBitmapValue, int](ruleix.All(rules...)).Build(ruleix.Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			query := duplicateBitmapValue{}
			for child := range children {
				query.values[child] = 1
			}

			b.Run("Cold", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					local := index.Local()
					benchmarkIntResult = benchmarkIntResult[:0]
					local.Search(query, &benchmarkIntResult)
					local.Close()
				}
			})
			b.Run("Warm", func(b *testing.B) {
				local := index.Local()
				defer local.Close()
				for range 3 {
					benchmarkIntResult = benchmarkIntResult[:0]
					local.Search(query, &benchmarkIntResult)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					benchmarkIntResult = benchmarkIntResult[:0]
					local.Search(query, &benchmarkIntResult)
				}
			})
		})
	}
}

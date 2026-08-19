// Command local_search demonstrates repeated searches with a local cache.
package main

import (
	"fmt"

	"github.com/typomaker/ruleix"
)

type constraint struct {
	store  *int
	region *int
}

func pointer(value int) *int { return &value }

func main() {
	entries := ruleix.Zip(
		[]constraint{
			{},
			{store: pointer(10)},
			{store: pointer(10), region: pointer(20)},
			{store: pointer(10), region: pointer(30)},
		},
		[]string{"global", "store", "region-20", "region-30"},
	)
	index, err := ruleix.New[constraint, string](ruleix.All(
		ruleix.Include(func(c constraint) (int, bool) { return optional(c.store) }),
		ruleix.Include(func(c constraint) (int, bool) { return optional(c.region) }),
	)).Build(entries)
	if err != nil {
		panic(err)
	}

	// Local belongs to this goroutine and retains two recent intermediate
	// bitmaps per filter. The immutable index may still be shared globally.
	local := index.Local()
	var matches []string
	for _, region := range []int{20, 30, 20} {
		local.Search(constraint{store: pointer(10), region: pointer(region)}, &matches)
		fmt.Printf("region %d: %v\n", region, matches)
	}
}

func optional(value *int) (int, bool) {
	if value == nil {
		return 0, false
	}
	return *value, true
}

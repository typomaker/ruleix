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
	entries, err := ruleix.Zip(
		[]constraint{
			{},
			{store: pointer(10)},
			{store: pointer(10), region: pointer(20)},
			{store: pointer(10), region: pointer(30)},
		},
		[]string{"global", "store", "region-20", "region-30"},
	)
	if err != nil {
		panic(err)
	}
	index, err := ruleix.New[constraint, string](ruleix.All(
		ruleix.Include(func(c constraint) *int { return c.store }),
		ruleix.Include(func(c constraint) *int { return c.region }),
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

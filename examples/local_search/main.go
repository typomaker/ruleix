// Command local_search demonstrates a batch of related searches.
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

	regions := []int{20, 30, 20}
	conditions := make([]constraint, len(regions))
	for i, region := range regions {
		conditions[i] = constraint{store: pointer(10), region: pointer(region)}
	}
	found := make([][]string, len(conditions))
	index.Search(conditions, found)
	for i, matches := range found {
		fmt.Printf("region %d: %v\n", regions[i], matches)
	}
}

func optional(value *int) (int, bool) {
	if value == nil {
		return 0, false
	}
	return *value, true
}

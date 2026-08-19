// Package main demonstrates matching rules across several independent columns.
package main

import (
	"cmp"
	"fmt"

	"github.com/typomaker/ruleix"
)

type constraint struct {
	country         *string
	tier            *string
	minimumTotal    *int
	excludedChannel *string
}

func main() {
	schema := ruleix.All(
		ruleix.Include(func(c constraint) (string, bool) { return optional(c.country) }),
		ruleix.Include(func(c constraint) (string, bool) { return optional(c.tier) }),
		ruleix.GreaterOrEqual(func(c constraint) (int, bool) { return optional(c.minimumTotal) }, cmp.Compare[int]),
		ruleix.Exclude(func(c constraint) (string, bool) { return optional(c.excludedChannel) }),
	)

	constraints := []constraint{
		{
			country:         pointer("DE"),
			tier:            pointer("gold"),
			minimumTotal:    pointer(100),
			excludedChannel: pointer("marketplace"),
		},
		{country: pointer("DE")},
		{},
	}
	ids := []string{"gold-de", "all-de", "global"}

	entries := ruleix.Zip(constraints, ids)
	index, err := ruleix.New[constraint, string](schema).Build(entries)
	if err != nil {
		panic(err)
	}

	printMatches(index, constraint{
		country:         pointer("DE"),
		tier:            pointer("gold"),
		minimumTotal:    pointer(150),
		excludedChannel: pointer("web"),
	})
	printMatches(index, constraint{
		country:         pointer("DE"),
		tier:            pointer("gold"),
		minimumTotal:    pointer(150),
		excludedChannel: pointer("marketplace"),
	})

	// Output:
	// [gold-de all-de global]
	// [all-de global]
}

func printMatches(index *ruleix.Index[constraint, string], query constraint) {
	var matches []string
	index.Search(query, &matches)
	fmt.Println(matches)
}

func pointer[T any](value T) *T { return &value }
func optional[T any](value *T) (T, bool) {
	if value == nil {
		var zero T
		return zero, false
	}
	return *value, true
}

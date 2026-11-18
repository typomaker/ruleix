//nolint:lll // Migration examples keep legacy pointer getters inline.
package ruleix_test

import (
	"cmp"
	"fmt"

	"github.com/typomaker/ruleix"
)

func Example_multiColumnRules() {
	type targetingConstraint struct {
		country         *string
		tier            *string
		minimumTotal    *int
		excludedChannel *string
	}

	schema := ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(c targetingConstraint) *string { return c.country })),
		ruleix.Include(ruleix.GetterFromPointer(func(c targetingConstraint) *string { return c.tier })),
		ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(c targetingConstraint) *int { return c.minimumTotal }), cmp.Compare[int]),
		ruleix.Exclude(ruleix.GetterFromPointer(func(c targetingConstraint) *string { return c.excludedChannel })),
	)

	countryDE := "DE"
	tierGold := "gold"
	minimum100 := 100
	marketplace := "marketplace"
	constraints := []targetingConstraint{
		{
			country:         &countryDE,
			tier:            &tierGold,
			minimumTotal:    &minimum100,
			excludedChannel: &marketplace,
		},
		{country: &countryDE},
		{},
	}
	ids := []string{"gold-de", "all-de", "global"}

	entries := ruleix.Zip(constraints, ids)
	index, err := ruleix.New[targetingConstraint, string](schema).Build(entries)
	if err != nil {
		panic(err)
	}

	minimum150 := 150
	web := "web"
	query := targetingConstraint{
		country:         &countryDE,
		tier:            &tierGold,
		minimumTotal:    &minimum150,
		excludedChannel: &web,
	}
	found := make([][]string, 1)
	index.Search([]targetingConstraint{query}, found)
	matches := found[0]
	fmt.Println(matches)

	// Output: [gold-de all-de global]
}

func ExampleIndex_Search() {
	type constraint struct {
		store  *int
		region *int
	}
	pointer := func(value int) *int { return &value }

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
		ruleix.Include(ruleix.GetterFromPointer(func(c constraint) *int { return c.store })),
		ruleix.Include(ruleix.GetterFromPointer(func(c constraint) *int { return c.region })),
	)).Build(entries)
	if err != nil {
		panic(err)
	}

	conditions := []constraint{
		{store: pointer(10), region: pointer(20)},
		{store: pointer(10), region: pointer(30)},
	}
	found := make([][]string, len(conditions))
	index.Search(conditions, found)
	for _, matches := range found {
		fmt.Println(matches)
	}

	// Output:
	// [global store region-20]
	// [global store region-30]
}

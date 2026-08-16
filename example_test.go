package ruleix_test

import (
	"cmp"
	"fmt"

	"github.com/albertsultanov/ruleix"
)

func Example_multiColumnRules() {
	type targetingConstraint struct {
		country         *string
		tier            *string
		minimumTotal    *int
		excludedChannel *string
	}

	schema := ruleix.All(
		ruleix.Include(func(c targetingConstraint) *string { return c.country }),
		ruleix.Include(func(c targetingConstraint) *string { return c.tier }),
		ruleix.GreaterOrEqual(func(c targetingConstraint) *int { return c.minimumTotal }, cmp.Compare[int]),
		ruleix.Exclude(func(c targetingConstraint) *string { return c.excludedChannel }),
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

	entries, err := ruleix.Zip(constraints, ids)
	if err != nil {
		panic(err)
	}
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
	var matches []string
	index.Search(query, &matches)
	fmt.Println(matches)

	// Output: [gold-de all-de global]
}

func ExampleIndex_Local() {
	type constraint struct {
		store  *int
		region *int
	}
	pointer := func(value int) *int { return &value }

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

	// Local caches intermediate bitmaps between calls. Keep it in one goroutine.
	local := index.Local()
	var matches []string
	for _, region := range []int{20, 30} {
		local.Search(constraint{store: pointer(10), region: pointer(region)}, &matches)
		fmt.Println(matches)
	}

	// Output:
	// [global store region-20]
	// [global store region-30]
}

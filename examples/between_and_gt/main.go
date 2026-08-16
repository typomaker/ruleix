// Package main demonstrates combining Between with a strict greater-than rule.
package main

import (
	"cmp"
	"fmt"
	"time"

	"github.com/albertsultanov/ruleix"
)

type constraint struct {
	active     *timeRange
	orderCount *orderCountRule
}

type timeRange struct {
	from  *time.Time
	until *time.Time
}

type orderCountRule struct {
	operator string
	value    *int
}

func main() {
	schema := ruleix.All(
		ruleix.BetweenConstraint(
			func(c constraint) *timeRange { return c.active },
			func(r timeRange) *time.Time { return r.from },
			func(r timeRange) *time.Time { return r.until },
			time.Time.Compare,
		),
		ruleix.CompareBy(
			func(c constraint) string {
				if c.orderCount == nil {
					return ""
				}
				return c.orderCount.operator
			},
			ruleix.Path(
				func(c constraint) *orderCountRule { return c.orderCount },
				func(r orderCountRule) *int { return r.value },
			),
			cmp.Compare[int],
		),
	)

	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	ten := 10
	constraints := []constraint{
		{
			active:     &timeRange{from: &start, until: &end},
			orderCount: &orderCountRule{operator: ">", value: &ten},
		},
		{},
	}
	ids := []string{"january-returning-customer", "global"}

	entries, err := ruleix.Zip(constraints, ids)
	if err != nil {
		panic(err)
	}
	index, err := ruleix.New[constraint, string](schema).Build(entries)
	if err != nil {
		panic(err)
	}

	queryFrom := start.Add(7 * 24 * time.Hour)
	queryUntil := start.Add(14 * 24 * time.Hour)
	eleven := 11
	query := constraint{
		active:     &timeRange{from: &queryFrom, until: &queryUntil},
		orderCount: &orderCountRule{value: &eleven},
	}

	var matches []string
	index.Search(query, &matches)
	fmt.Println(matches)

	// Output: [january-returning-customer global]
}

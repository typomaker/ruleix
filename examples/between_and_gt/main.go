// Package main demonstrates combining Between with a strict greater-than rule.
package main

import (
	"cmp"
	"fmt"
	"time"

	"github.com/typomaker/ruleix"
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
	operator *ruleix.Operator
	value    *int
}

func main() {
	schema := ruleix.All(
		ruleix.Between(func(c constraint) (time.Time, bool) {
			if c.active == nil || c.active.from == nil {
				return time.Time{}, false
			}
			return *c.active.from, true
		}, func(c constraint) (time.Time, bool) {
			if c.active == nil || c.active.until == nil {
				return time.Time{}, false
			}
			return *c.active.until, true
		}, time.Time.Compare,
		),
		ruleix.CompareBy(func(c constraint) (int, bool) {
			if c.orderCount == nil || c.orderCount.value == nil {
				return 0, false
			}
			return *c.orderCount.value, true
		}, func(c constraint) (ruleix.Operator, bool) {
			if c.orderCount == nil || c.orderCount.operator == nil {
				return 0, false
			}
			return *c.orderCount.operator, true
		}, cmp.Compare[int],
		),
	)

	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	ten := 10
	greater := ruleix.OperatorGT
	constraints := []constraint{
		{
			active:     &timeRange{from: &start, until: &end},
			orderCount: &orderCountRule{operator: &greater, value: &ten},
		},
		{},
	}
	ids := []string{"january-returning-customer", "global"}

	entries := ruleix.Zip(constraints, ids)
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

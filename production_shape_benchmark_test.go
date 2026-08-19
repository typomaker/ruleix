//nolint:lll // Production-shaped getters stay inline for benchmark fidelity.
package ruleix_test

import (
	"cmp"
	"encoding/binary"
	"testing"
	"time"

	"github.com/typomaker/ruleix"
)

const productionBenchmarkEntries = 38_098

type productionBenchmarkID [16]byte

type productionBenchmarkTimeRange struct {
	since time.Time
	until time.Time
}

type productionBenchmarkOrderCount struct {
	total int
}

type productionBenchmarkVersion struct {
	major int
	minor int
	patch int
}

type productionBenchmarkPlatform struct {
	name    string
	version *productionBenchmarkVersion
}

type productionBenchmarkConstraint struct {
	activity           *productionBenchmarkTimeRange
	customerOrderCount *productionBenchmarkOrderCount
	slotTime           *productionBenchmarkTimeRange
	customerUUID       *[16]byte
	customerSegment    *uint8
	customerFraud      *bool
	storeUUID          *[16]byte
	deliveryAreaID     *int
	regionID           *int
	retailerUUID       *[16]byte
	vertical           *uint8
	slotType           *uint8
	slotDayOfWeek      *int
	platform           *productionBenchmarkPlatform
	dbs                *bool
	marketType         *uint8
	abTest             *[2]string
}

var productionBenchmarkIndexResult *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID]

func productionBenchmarkSchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		productionBenchmarkActivityRule(),
		ruleix.CompareBy(func(value productionBenchmarkConstraint) (int, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return value.customerOrderCount.total, true
		}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return ruleix.OperatorGTE, true
		}, cmp.Compare[int],
		),
		productionBenchmarkSlotTimeRule(),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.customerUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.customerSegment)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (bool, bool) { return benchmarkOptional(value.customerFraud) }),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) { return benchmarkOptional(value.storeUUID) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) { return benchmarkOptional(value.deliveryAreaID) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) { return benchmarkOptional(value.regionID) }),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.retailerUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) { return benchmarkOptional(value.vertical) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) { return benchmarkOptional(value.slotType) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) { return benchmarkOptional(value.slotDayOfWeek) }),
		productionBenchmarkPlatformRule(),
		ruleix.Include(func(value productionBenchmarkConstraint) (bool, bool) { return benchmarkOptional(value.dbs) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) { return benchmarkOptional(value.marketType) }),
		ruleix.Include(func(value productionBenchmarkConstraint) ([2]string, bool) { return benchmarkOptional(value.abTest) }),
	)
}

func benchmarkOptional[V any](value *V) (V, bool) {
	if value == nil {
		var zero V
		return zero, false
	}
	return *value, true
}

func productionBenchmarkActivityRule() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.Between(func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.activity == nil {
			return time.Time{}, false
		}
		return value.activity.since.Truncate(time.Second), true
	}, func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.activity == nil {
			return time.Time{}, false
		}
		return value.activity.until.Truncate(time.Second), true
	}, time.Time.Compare,
	)
}

func productionBenchmarkSlotTimeRule() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.Between(func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.slotTime == nil {
			return time.Time{}, false
		}
		return value.slotTime.since.Truncate(time.Second), true
	}, func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.slotTime == nil {
			return time.Time{}, false
		}
		return value.slotTime.until.Truncate(time.Second), true
	}, time.Time.Compare,
	)
}

func productionBenchmarkPlatformRule() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		ruleix.Include(func(value productionBenchmarkConstraint) (string, bool) {
			if value.platform == nil {
				return "", false
			}
			return value.platform.name, true
		}),
		ruleix.CompareBy(func(value productionBenchmarkConstraint) ([3]int, bool) {
			if value.platform == nil || value.platform.version == nil {
				return [3]int{}, false
			}
			version := value.platform.version
			return [3]int{version.major, version.minor, version.patch}, true
		}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
			if value.platform == nil || value.platform.version == nil {
				return 0, false
			}
			return ruleix.OperatorGTE, true
		}, func(a, b [3]int) int {
			for i := range a {
				if result := cmp.Compare(a[i], b[i]); result != 0 {
					return result
				}
			}
			return 0
		},
		),
	)
}

func productionBenchmarkData() ([]productionBenchmarkConstraint, []productionBenchmarkID) {
	constraints := make([]productionBenchmarkConstraint, productionBenchmarkEntries)
	ids := make([]productionBenchmarkID, productionBenchmarkEntries)
	customerUUIDs := make([][16]byte, 536)
	storeUUIDs := make([][16]byte, 179)
	for i := range customerUUIDs {
		binary.BigEndian.PutUint64(customerUUIDs[i][8:], uint64(i))
	}
	for i := range storeUUIDs {
		binary.BigEndian.PutUint64(storeUUIDs[i][8:], uint64(i))
	}
	baseTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := range constraints {
		binary.BigEndian.PutUint64(ids[i][8:], uint64(i))
		if i < 38_074 {
			day := i % 365
			constraints[i].activity = &productionBenchmarkTimeRange{
				since: baseTime.AddDate(0, 0, day),
				until: baseTime.AddDate(0, 0, day+7),
			}
		}
		if i < 38_095 {
			constraints[i].customerOrderCount = &productionBenchmarkOrderCount{total: 1}
		}
		if i < 33_176 {
			constraints[i].customerUUID = &customerUUIDs[i%len(customerUUIDs)]
			constraints[i].slotType = ptr(uint8(i%2 + 1))
		}
		if i < 4_919 {
			constraints[i].storeUUID = &storeUUIDs[i%len(storeUUIDs)]
		}
		if i < 24 {
			constraints[i].regionID = ptr(1)
		}
		if i < 38_097 {
			constraints[i].platform = &productionBenchmarkPlatform{name: productionPlatformName(i % 34)}
			if i < 6_163 {
				constraints[i].platform.version = &productionBenchmarkVersion{major: i%3 + 1}
			}
		}
		if i < 38_074 {
			constraints[i].dbs = ptr(i%2 == 0)
			constraints[i].marketType = ptr(uint8(1))
		}
	}
	return constraints, ids
}

func productionPlatformName(value int) string {
	return string([]byte{'p', byte('a' + value/26), byte('a' + value%26)})
}

func productionBenchmarkQuery(day int) productionBenchmarkConstraint {
	baseTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return productionBenchmarkConstraint{
		activity:           &productionBenchmarkTimeRange{since: baseTime.AddDate(0, 0, day)},
		customerOrderCount: &productionBenchmarkOrderCount{total: 1},
		customerUUID:       ptr([16]byte{}),
		storeUUID:          ptr([16]byte{}),
		regionID:           ptr(1),
		slotType:           ptr(uint8(1)),
		platform: &productionBenchmarkPlatform{
			name: "paa", version: &productionBenchmarkVersion{major: 2},
		},
		dbs:        ptr(true),
		marketType: ptr(uint8(1)),
	}
}

func BenchmarkProductionShapeSearch(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for _, local := range []bool{false, true} {
		name := "Index"
		if local {
			name = "Local"
		}
		b.Run(name, func(b *testing.B) {
			searcher := index.Local()
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				query := queries[i%len(queries)]
				if local {
					searcher.Search(query, &matches)
				} else {
					index.Search(query, &matches)
				}
			}
		})
	}
}

func BenchmarkProductionShapeBuild(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index, err := builder.Build(ruleix.Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		productionBenchmarkIndexResult = index
	}
	b.ReportMetric(productionBenchmarkEntries, "entries/op")
}

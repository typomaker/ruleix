package ruleix_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

func productionLogicConstraint() productionBenchmarkConstraint {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return productionBenchmarkConstraint{
		activity: &productionBenchmarkTimeRange{
			since: base,
			until: base.Add(3 * time.Hour),
		},
		customerOrderCount: &productionBenchmarkOrderCount{total: 1},
		slotTime: &productionBenchmarkTimeRange{
			since: base.Add(4 * time.Hour),
			until: base.Add(4 * time.Hour),
		},
		customerUUID:    ptr([16]byte{1}),
		customerSegment: ptr(uint8(2)),
		customerFraud:   ptr(false),
		storeUUID:       ptr([16]byte{2}),
		deliveryAreaID:  ptr(10),
		regionID:        ptr(20),
		retailerUUID:    ptr([16]byte{3}),
		vertical:        ptr(uint8(4)),
		slotType:        ptr(uint8(1)),
		slotDayOfWeek:   ptr(3),
		platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 1, minor: 5},
		},
		dbs:        ptr(true),
		marketType: ptr(uint8(1)),
		abTest:     ptr([2]string{"checkout", "b"}),
	}
}

func productionLogicQuery() productionBenchmarkConstraint {
	query := productionLogicConstraint()
	base := query.activity.since
	query.activity = &productionBenchmarkTimeRange{
		since: base.Add(time.Hour),
		until: base.Add(2 * time.Hour),
	}
	query.customerOrderCount = &productionBenchmarkOrderCount{total: 2}
	query.platform = &productionBenchmarkPlatform{
		name: "ios", version: &productionBenchmarkVersion{major: 2},
	}
	return query
}

func productionTestID(value byte) productionBenchmarkID {
	var result productionBenchmarkID
	result[len(result)-1] = value
	return result
}

func assertProductionSearches(
	t *testing.T,
	index *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID],
	query productionBenchmarkConstraint,
	want []productionBenchmarkID,
) {
	t.Helper()
	var indexMatches []productionBenchmarkID
	index.Search(query, &indexMatches)
	require.Equal(t, want, indexMatches)

	var localMatches []productionBenchmarkID
	index.Local().Search(query, &localMatches)
	require.Equal(t, want, localMatches)
}

func TestProductionShapeAllFieldsRejectMismatches(t *testing.T) {
	baseTime := productionLogicConstraint().activity.since
	tests := []struct {
		name   string
		mutate func(*productionBenchmarkConstraint)
	}{
		{"activity", func(value *productionBenchmarkConstraint) {
			value.activity = &productionBenchmarkTimeRange{
				since: baseTime.Add(2 * time.Hour), until: baseTime.Add(3 * time.Hour),
			}
		}},
		{"customer order count", func(value *productionBenchmarkConstraint) {
			value.customerOrderCount = &productionBenchmarkOrderCount{total: 3}
		}},
		{"slot time", func(value *productionBenchmarkConstraint) {
			value.slotTime = &productionBenchmarkTimeRange{
				since: baseTime.Add(5 * time.Hour), until: baseTime.Add(5 * time.Hour),
			}
		}},
		{"customer UUID", func(value *productionBenchmarkConstraint) { value.customerUUID = ptr([16]byte{9}) }},
		{"customer segment", func(value *productionBenchmarkConstraint) { value.customerSegment = ptr(uint8(9)) }},
		{"customer fraud", func(value *productionBenchmarkConstraint) { value.customerFraud = ptr(true) }},
		{"store UUID", func(value *productionBenchmarkConstraint) { value.storeUUID = ptr([16]byte{9}) }},
		{"delivery area ID", func(value *productionBenchmarkConstraint) { value.deliveryAreaID = ptr(99) }},
		{"region ID", func(value *productionBenchmarkConstraint) { value.regionID = ptr(99) }},
		{"retailer UUID", func(value *productionBenchmarkConstraint) { value.retailerUUID = ptr([16]byte{9}) }},
		{"vertical", func(value *productionBenchmarkConstraint) { value.vertical = ptr(uint8(9)) }},
		{"slot type", func(value *productionBenchmarkConstraint) { value.slotType = ptr(uint8(9)) }},
		{"slot day of week", func(value *productionBenchmarkConstraint) { value.slotDayOfWeek = ptr(6) }},
		{"platform name", func(value *productionBenchmarkConstraint) {
			value.platform = &productionBenchmarkPlatform{name: "android"}
		}},
		{"platform version", func(value *productionBenchmarkConstraint) {
			value.platform = &productionBenchmarkPlatform{
				name: "ios", version: &productionBenchmarkVersion{major: 2, minor: 1},
			}
		}},
		{"DBS", func(value *productionBenchmarkConstraint) { value.dbs = ptr(false) }},
		{"market type", func(value *productionBenchmarkConstraint) { value.marketType = ptr(uint8(9)) }},
		{"AB test", func(value *productionBenchmarkConstraint) {
			value.abTest = ptr([2]string{"checkout", "a"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matching := productionLogicConstraint()
			mismatching := productionLogicConstraint()
			tt.mutate(&mismatching)
			index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
				productionBenchmarkSchema(),
			).Build(ruleix.Zip(
				[]productionBenchmarkConstraint{{}, matching, mismatching},
				[]productionBenchmarkID{productionTestID(1), productionTestID(2), productionTestID(3)},
			))
			require.NoError(t, err)

			assertProductionSearches(t, index, productionLogicQuery(), []productionBenchmarkID{
				productionTestID(1), productionTestID(2),
			})
		})
	}
}

func TestProductionShapeMissingQueryValuesMatchOnlyWildcards(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*productionBenchmarkConstraint)
	}{
		{"activity", func(value *productionBenchmarkConstraint) { value.activity = nil }},
		{"customer order count", func(value *productionBenchmarkConstraint) { value.customerOrderCount = nil }},
		{"slot time", func(value *productionBenchmarkConstraint) { value.slotTime = nil }},
		{"customer UUID", func(value *productionBenchmarkConstraint) { value.customerUUID = nil }},
		{"customer segment", func(value *productionBenchmarkConstraint) { value.customerSegment = nil }},
		{"customer fraud", func(value *productionBenchmarkConstraint) { value.customerFraud = nil }},
		{"store UUID", func(value *productionBenchmarkConstraint) { value.storeUUID = nil }},
		{"delivery area ID", func(value *productionBenchmarkConstraint) { value.deliveryAreaID = nil }},
		{"region ID", func(value *productionBenchmarkConstraint) { value.regionID = nil }},
		{"retailer UUID", func(value *productionBenchmarkConstraint) { value.retailerUUID = nil }},
		{"vertical", func(value *productionBenchmarkConstraint) { value.vertical = nil }},
		{"slot type", func(value *productionBenchmarkConstraint) { value.slotType = nil }},
		{"slot day of week", func(value *productionBenchmarkConstraint) { value.slotDayOfWeek = nil }},
		{"platform", func(value *productionBenchmarkConstraint) { value.platform = nil }},
		{"platform version", func(value *productionBenchmarkConstraint) {
			value.platform = &productionBenchmarkPlatform{name: "ios"}
		}},
		{"DBS", func(value *productionBenchmarkConstraint) { value.dbs = nil }},
		{"market type", func(value *productionBenchmarkConstraint) { value.marketType = nil }},
		{"AB test", func(value *productionBenchmarkConstraint) { value.abTest = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
				productionBenchmarkSchema(),
			).Build(ruleix.Zip(
				[]productionBenchmarkConstraint{{}, productionLogicConstraint()},
				[]productionBenchmarkID{productionTestID(1), productionTestID(2)},
			))
			require.NoError(t, err)
			query := productionLogicQuery()
			tt.mutate(&query)

			assertProductionSearches(t, index, query, []productionBenchmarkID{productionTestID(1)})
		})
	}
}

func TestProductionShapePlatformMatching(t *testing.T) {
	id := productionTestID
	constraints := []productionBenchmarkConstraint{
		{},
		{platform: &productionBenchmarkPlatform{name: "ios"}},
		{platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 1},
		}},
		{platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 2},
		}},
		{platform: &productionBenchmarkPlatform{
			name: "ios", version: &productionBenchmarkVersion{major: 2, minor: 1},
		}},
		{platform: &productionBenchmarkPlatform{name: "android"}},
	}
	ids := []productionBenchmarkID{id(1), id(2), id(3), id(4), id(5), id(6)}
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	require.NoError(t, err)

	tests := []struct {
		name  string
		query productionBenchmarkConstraint
		want  []productionBenchmarkID
	}{
		{
			name: "missing name matches only the platform wildcard",
			want: []productionBenchmarkID{id(1)},
		},
		{
			name: "name with missing version includes the matching name version wildcard",
			query: productionBenchmarkConstraint{platform: &productionBenchmarkPlatform{
				name: "ios",
			}},
			want: []productionBenchmarkID{id(1), id(2)},
		},
		{
			name: "name and version include matching name and satisfied version constraints",
			query: productionBenchmarkConstraint{platform: &productionBenchmarkPlatform{
				name: "ios", version: &productionBenchmarkVersion{major: 2},
			}},
			want: []productionBenchmarkID{id(1), id(2), id(3), id(4)},
		},
		{
			name: "different name excludes constraints for other platforms",
			query: productionBenchmarkConstraint{platform: &productionBenchmarkPlatform{
				name: "android", version: &productionBenchmarkVersion{major: 100},
			}},
			want: []productionBenchmarkID{id(1), id(6)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var indexMatches []productionBenchmarkID
			index.Search(tt.query, &indexMatches)
			require.Equal(t, tt.want, indexMatches)

			var localMatches []productionBenchmarkID
			index.Local().Search(tt.query, &localMatches)
			require.Equal(t, tt.want, localMatches)
		})
	}
}

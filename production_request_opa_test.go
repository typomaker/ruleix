package ruleix_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/storage/inmem"
	"github.com/stretchr/testify/require"
)

const productionOPAPolicy = `package ruleix

matching_ids contains constraint.id if {
	some constraint in data.constraints
	matches(constraint, input)
}

matches(c, q) if {
	range_contains(c, q, "activity_from", "activity_until")
	lower_matches(c, q, "customer_order_count")
	range_contains(c, q, "slot_time_from", "slot_time_until")
	every key in [
		"customer_uuid", "customer_segment", "customer_fraud", "store_uuid",
		"delivery_area_id", "region_id", "retailer_uuid", "vertical", "slot_type",
		"slot_day_of_week", "platform_name", "dbs", "market_type", "ab_test",
	] { equality_matches(c, q, key) }
	lower_matches(c, q, "platform_version")
}

equality_matches(c, _, key) if { object.get(c, key, null) == null }
equality_matches(c, q, key) if {
	value := object.get(c, key, null)
	value != null
	value == object.get(q, key, null)
}

lower_matches(c, _, key) if { object.get(c, key, null) == null }
lower_matches(c, q, key) if {
	value := object.get(c, key, null)
	query := object.get(q, key, null)
	query != null
	value <= query
}

upper_matches(c, _, key) if { object.get(c, key, null) == null }
upper_matches(c, q, key) if {
	value := object.get(c, key, null)
	query := object.get(q, key, null)
	query != null
	query <= value
}

range_contains(c, q, lower, upper) if {
	lower_matches(c, q, lower)
	upper_matches(c, q, upper)
}`

type productionOPAMatcher struct {
	prepared rego.PreparedEvalQuery
	inputs   map[productionOPAQueryKey]map[string]any
	ids      []productionBenchmarkID
}

type productionOPAQueryKey struct {
	activity        int64
	customer, store [16]byte
	platform        string
	version         [3]int
	slot            uint8
	dbs             bool
}

func newProductionOPAMatcher(t testing.TB, constraints []productionBenchmarkConstraint, ids []productionBenchmarkID, queries []productionBenchmarkConstraint) *productionOPAMatcher {
	t.Helper()
	rows := make([]any, len(constraints))
	for i, constraint := range constraints {
		row := productionOPAValue(constraint)
		row["id"] = strconv.Itoa(i)
		rows[i] = row
	}
	prepared, err := rego.New(
		rego.Query("data.ruleix.matching_ids"),
		rego.Module("production_request.rego", productionOPAPolicy),
		rego.Store(inmem.NewFromObject(map[string]any{"constraints": rows})),
	).PrepareForEval(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inputs := make(map[productionOPAQueryKey]map[string]any, len(queries))
	for _, query := range queries {
		inputs[productionOPAKey(query)] = productionOPAValue(query)
	}
	return &productionOPAMatcher{prepared: prepared, inputs: inputs, ids: ids}
}

func (m *productionOPAMatcher) Match(query productionBenchmarkConstraint, dst *[]productionBenchmarkID) {
	result, err := m.prepared.Eval(context.Background(), rego.EvalInput(m.inputs[productionOPAKey(query)]))
	if err != nil {
		panic(err)
	}
	if len(result) != 1 || len(result[0].Expressions) != 1 {
		panic(fmt.Sprintf("unexpected OPA result: %#v", result))
	}
	values, ok := result[0].Expressions[0].Value.([]any)
	if !ok {
		panic(fmt.Sprintf("unexpected OPA value: %T", result[0].Expressions[0].Value))
	}
	for _, value := range values {
		index, err := strconv.Atoi(value.(string))
		if err != nil {
			panic(err)
		}
		*dst = append(*dst, m.ids[index])
	}
}

func productionOPAKey(value productionBenchmarkConstraint) productionOPAQueryKey {
	var key productionOPAQueryKey
	if value.activity != nil {
		key.activity = value.activity.since.Unix()
	}
	if value.customerUUID != nil {
		key.customer = *value.customerUUID
	}
	if value.storeUUID != nil {
		key.store = *value.storeUUID
	}
	if value.platform != nil {
		key.platform = value.platform.name
		if value.platform.version != nil {
			key.version = [3]int{value.platform.version.major, value.platform.version.minor, value.platform.version.patch}
		}
	}
	if value.slotType != nil {
		key.slot = *value.slotType
	}
	if value.dbs != nil {
		key.dbs = *value.dbs
	}
	return key
}

func productionOPAValue(value productionBenchmarkConstraint) map[string]any {
	result := make(map[string]any, 20)
	if value.activity != nil {
		result["activity_from"], result["activity_until"] = value.activity.since.Unix(), value.activity.until.Unix()
	}
	if value.customerOrderCount != nil {
		result["customer_order_count"] = value.customerOrderCount.total
	}
	if value.slotTime != nil {
		result["slot_time_from"], result["slot_time_until"] = value.slotTime.since.Unix(), value.slotTime.until.Unix()
	}
	productionOPAOptional(result, "customer_uuid", value.customerUUID, func(v [16]byte) any { return hex.EncodeToString(v[:]) })
	productionOPAOptional(result, "customer_segment", value.customerSegment, func(v uint8) any { return int(v) })
	productionOPAOptional(result, "customer_fraud", value.customerFraud, func(v bool) any { return v })
	productionOPAOptional(result, "store_uuid", value.storeUUID, func(v [16]byte) any { return hex.EncodeToString(v[:]) })
	productionOPAOptional(result, "delivery_area_id", value.deliveryAreaID, func(v int) any { return v })
	productionOPAOptional(result, "region_id", value.regionID, func(v int) any { return v })
	productionOPAOptional(result, "retailer_uuid", value.retailerUUID, func(v [16]byte) any { return hex.EncodeToString(v[:]) })
	productionOPAOptional(result, "vertical", value.vertical, func(v uint8) any { return int(v) })
	productionOPAOptional(result, "slot_type", value.slotType, func(v uint8) any { return int(v) })
	productionOPAOptional(result, "slot_day_of_week", value.slotDayOfWeek, func(v int) any { return v })
	if value.platform != nil {
		result["platform_name"] = value.platform.name
		if value.platform.version != nil {
			result["platform_version"] = []any{value.platform.version.major, value.platform.version.minor, value.platform.version.patch}
		}
	}
	productionOPAOptional(result, "dbs", value.dbs, func(v bool) any { return v })
	productionOPAOptional(result, "market_type", value.marketType, func(v uint8) any { return int(v) })
	productionOPAOptional(result, "ab_test", value.abTest, func(v [2]string) any { return []any{v[0], v[1]} })
	return result
}

func productionOPAOptional[V any](dst map[string]any, key string, value *V, encode func(V) any) {
	if value != nil {
		dst[key] = encode(*value)
	}
}

func TestProductionOPAProducesSameResults(t *testing.T) {
	constraints, ids := productionBenchmarkData()
	queries := productionRequestQueries("Correlated", false)
	if os.Getenv("RULEIX_OPA_FULL_CORRECTNESS") == "" {
		queries = queries[:8]
	}
	opa := newProductionOPAMatcher(t, constraints, ids, queries)
	linear := &productionLinearMatcher{constraints: constraints, ids: ids, optimized: true}
	for index, query := range queries {
		var expected, actual []productionBenchmarkID
		linear.Match(query, &expected)
		opa.Match(query, &actual)
		require.Equalf(t, productionSortedIDs(expected), productionSortedIDs(actual), "query %d", index)
	}
}

// benchmarkRequestOPA measures prepared OPA collect-all evaluation only; data
// conversion, store creation, and Rego compilation are outside child timers.
// Latest local check (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, OPA v1.13.2,
// 38,098 constraints, Correlated/LargeWorkingSet, one lookup, 1x x3): median
// 485 ms/request, 252,343,248 B/request, and 4,526,821 allocs/request.
func benchmarkRequestOPA(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	for _, mode := range []string{"Independent", "Correlated"} {
		for _, cache := range []struct {
			name string
			hot  bool
		}{{"HotContext", true}, {"LargeWorkingSet", false}} {
			queries := productionRequestQueries(mode, cache.hot)
			matcher := newProductionOPAMatcher(b, constraints, ids, queries)
			for _, lookups := range []int{1, 10, 25, 50, 100} {
				b.Run(fmt.Sprintf("40K/%d/%s/%s", lookups, mode, cache.name), func(b *testing.B) { benchmarkProductionRequest(b, matcher, queries, lookups) })
			}
		}
	}
}

func benchmarkRequestParallelOPA(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	queries := productionRequestQueries("Correlated", false)
	matcher := newProductionOPAMatcher(b, constraints, ids, queries)
	for _, lookups := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("40K/%d", lookups), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				results, request := make([]productionBenchmarkID, 0, productionBenchmarkEntries), 0
				for pb.Next() {
					base := request * lookups % len(queries)
					for j := range lookups {
						results = results[:0]
						matcher.Match(queries[(base+j)%len(queries)], &results)
					}
					request++
				}
			})
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "requests/s")
			b.ReportMetric(float64(b.N*lookups)/b.Elapsed().Seconds(), "lookups/s")
		})
	}
}

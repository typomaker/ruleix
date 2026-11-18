package ruleix_test

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/typomaker/ruleix"
)

type productionDumpRange struct {
	since time.Time
	until time.Time
}

func (r *productionDumpRange) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	since, until, ok := strings.Cut(value, `","`)
	if !ok || !strings.HasPrefix(since, `("`) || !strings.HasSuffix(until, `")`) {
		return fmt.Errorf("invalid timestamp pair %q", value)
	}
	const layout = "2006-01-02 15:04:05Z07"
	var err error
	r.since, err = time.Parse(layout, since[2:])
	if err != nil {
		return err
	}
	r.until, err = time.Parse(layout, until[:len(until)-2])
	return err
}

// These indexed columns are NULL throughout this snapshot. Reject other
// snapshots that would require decoding their production-specific types.
type productionDumpEmptyFields struct {
	OrderCount         *json.RawMessage `json:"customer_order_count"`
	CustomerUUIDNot    *json.RawMessage `json:"customer_uuid_not"`
	CustomerSegment    *json.RawMessage `json:"customer_segment"`
	CustomerSegmentNot *json.RawMessage `json:"customer_segment_not"`
	CustomerFraud      *json.RawMessage `json:"customer_fraud"`
	StoreUUIDNot       *json.RawMessage `json:"store_uuid_not"`
	DeliveryAreaID     *json.RawMessage `json:"delivery_area_id"`
	RegionID           *json.RawMessage `json:"region_id"`
	RegionIDNot        *json.RawMessage `json:"region_id_not"`
	RetailerUUID       *json.RawMessage `json:"retailer_uuid"`
	RetailerUUIDNot    *json.RawMessage `json:"retailer_uuid_not"`
	Vertical           *json.RawMessage `json:"vertical"`
	VerticalNot        *json.RawMessage `json:"vertical_not"`
	PlatformName       *json.RawMessage `json:"platform_name"`
	PlatformVersion    *json.RawMessage `json:"platform_version"`
	DBS                *json.RawMessage `json:"dbs"`
	MarketType         *json.RawMessage `json:"market_type"`
	ABTest             *json.RawMessage `json:"abtest"`
	ABTestNot          *json.RawMessage `json:"abtest_not"`
}

type productionDumpOffer struct {
	StoreUUID uuid.UUID `json:"store_uuid"`
	SlotDay   string    `json:"slot_day_of_week"`
	SlotType  string    `json:"slot_type"`
}

type productionDumpConstraint struct {
	productionDumpOffer
	CustomerUUID uuid.UUID           `json:"customer_uuid"`
	Activity     productionDumpRange `json:"activity"`
	SlotTime     productionDumpRange `json:"slot_time"`
}

func readProductionDump(t testing.TB, input io.Reader) ([]productionDumpConstraint, []uuid.UUID) {
	t.Helper()
	decoder := json.NewDecoder(input)
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('[') {
		t.Fatalf("read production dump array: got %v, %v", opening, err)
	}

	var constraints []productionDumpConstraint
	var ids []uuid.UUID
	for decoder.More() {
		var row struct {
			productionDumpConstraint
			productionDumpEmptyFields
			ModifierUUID uuid.UUID `json:"modifier_uuid"`
		}
		if err := decoder.Decode(&row); err != nil {
			t.Fatalf("decode production dump row %d: %v", len(constraints)+1, err)
		}
		if row.productionDumpEmptyFields != (productionDumpEmptyFields{}) {
			t.Fatalf("production dump row %d has values in unsupported indexed columns", len(constraints)+1)
		}
		if row.ModifierUUID == uuid.Nil || row.CustomerUUID == uuid.Nil || row.StoreUUID == uuid.Nil ||
			row.SlotDay == "" || row.SlotType == "" ||
			!row.Activity.since.Before(row.Activity.until) || !row.SlotTime.since.Before(row.SlotTime.until) {
			t.Fatalf("production dump row %d is missing a required ID, slot field, or time range", len(constraints)+1)
		}
		constraints = append(constraints, row.productionDumpConstraint)
		ids = append(ids, row.ModifierUUID)
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("close production dump array: %v", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("expected EOF after production dump array, got %v", err)
	}
	if len(constraints) == 0 {
		t.Fatal("empty production dump")
	}
	return constraints, ids
}

func productionDumpBetween(
	get func(productionDumpConstraint) productionDumpRange,
) ruleix.Rule[productionDumpConstraint] {
	return ruleix.Between(
		func(c productionDumpConstraint) (time.Time, bool) { return get(c).since.Truncate(time.Second), true },
		func(c productionDumpConstraint) (time.Time, bool) { return get(c).until.Truncate(time.Second), true },
		time.Time.Compare,
	)
}

func productionDumpAbsent[V any](productionDumpConstraint) (V, bool) {
	var zero V
	return zero, false
}

func productionDumpSchema() ruleix.Rule[productionDumpConstraint] {
	return ruleix.All(
		productionDumpBetween(func(c productionDumpConstraint) productionDumpRange { return c.Activity }),
		ruleix.CompareBy(productionDumpAbsent[int], productionDumpAbsent[ruleix.Operator], cmp.Compare[int]),
		productionDumpBetween(func(c productionDumpConstraint) productionDumpRange { return c.SlotTime }),
		ruleix.Include(func(c productionDumpConstraint) (uuid.UUID, bool) { return c.CustomerUUID, true }),
		ruleix.Include(productionDumpAbsent[uuid.UUID]), // CustomerUUIDNot
		ruleix.Include(productionDumpAbsent[string]),    // CustomerSegment
		ruleix.Include(productionDumpAbsent[string]),    // CustomerSegmentNot
		ruleix.Include(productionDumpAbsent[bool]),      // CustomerFraud
		ruleix.Include(func(c productionDumpConstraint) (uuid.UUID, bool) { return c.StoreUUID, true }),
		ruleix.Include(productionDumpAbsent[uuid.UUID]), // StoreUUIDNot
		ruleix.Include(productionDumpAbsent[int]),       // DeliveryAreaID
		ruleix.Include(productionDumpAbsent[int]),       // RegionID
		ruleix.Include(productionDumpAbsent[int]),       // RegionIDNot
		ruleix.Include(productionDumpAbsent[uuid.UUID]), // RetailerUUID
		ruleix.Include(productionDumpAbsent[uuid.UUID]), // RetailerUUIDNot
		ruleix.Include(productionDumpAbsent[string]),    // Vertical
		ruleix.Include(productionDumpAbsent[string]),    // VerticalNot
		ruleix.Include(func(c productionDumpConstraint) (string, bool) { return c.SlotType, true }),
		ruleix.Include(func(c productionDumpConstraint) (string, bool) { return c.SlotDay, true }),
		ruleix.All(
			ruleix.Include(productionDumpAbsent[string]), // Platform.Name
			ruleix.CompareBy(productionDumpAbsent[[3]int], productionDumpAbsent[ruleix.Operator], compareProductionDumpVersion),
		),
		ruleix.Include(productionDumpAbsent[bool]),      // DBS
		ruleix.Include(productionDumpAbsent[string]),    // MarketType
		ruleix.Include(productionDumpAbsent[[2]string]), // ABTest
		ruleix.Include(productionDumpAbsent[[2]string]), // ABTestNot
	)
}

func compareProductionDumpVersion(a, b [3]int) int {
	for i := range a {
		if order := cmp.Compare(a[i], b[i]); order != 0 {
			return order
		}
	}
	return 0
}

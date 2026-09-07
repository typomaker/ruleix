package ruleix_test

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/typomaker/ruleix"
)

const productionRequestQueryCount = 2048

type productionRequestMatcher interface {
	Match(productionBenchmarkConstraint, *[]productionBenchmarkID)
}

type productionRequestScopedMatcher interface {
	BeginRequest()
	EndRequest()
}

func productionBeginRequest(matcher productionRequestMatcher) {
	if scoped, ok := matcher.(productionRequestScopedMatcher); ok {
		scoped.BeginRequest()
	}
}

func productionEndRequest(matcher productionRequestMatcher) {
	if scoped, ok := matcher.(productionRequestScopedMatcher); ok {
		scoped.EndRequest()
	}
}

type productionLinearMatcher struct {
	constraints []productionBenchmarkConstraint
	ids         []productionBenchmarkID
	optimized   bool
}

func (m *productionLinearMatcher) Match(q productionBenchmarkConstraint, dst *[]productionBenchmarkID) {
	for i := range m.constraints {
		matched := productionConstraintMatches(m.constraints[i], q, m.optimized)
		if matched {
			*dst = append(*dst, m.ids[i])
		}
	}
}

func productionConstraintMatches(c, q productionBenchmarkConstraint, optimized bool) bool {
	if optimized {
		return productionEqual(c.customerUUID, q.customerUUID) && productionEqual(c.storeUUID, q.storeUUID) &&
			productionPlatformMatches(c.platform, q.platform) && productionEqual(c.slotType, q.slotType) &&
			productionEqual(c.dbs, q.dbs) && productionEqual(c.marketType, q.marketType) &&
			productionNaturalRemainder(c, q)
	}
	return productionRangeMatches(c.activity, q.activity) && productionOrderMatches(c.customerOrderCount, q.customerOrderCount) &&
		productionRangeMatches(c.slotTime, q.slotTime) && productionEqual(c.customerUUID, q.customerUUID) &&
		productionEqual(c.customerSegment, q.customerSegment) && productionEqual(c.customerFraud, q.customerFraud) &&
		productionEqual(c.storeUUID, q.storeUUID) && productionEqual(c.deliveryAreaID, q.deliveryAreaID) &&
		productionEqual(c.regionID, q.regionID) && productionEqual(c.retailerUUID, q.retailerUUID) &&
		productionEqual(c.vertical, q.vertical) && productionEqual(c.slotType, q.slotType) &&
		productionEqual(c.slotDayOfWeek, q.slotDayOfWeek) && productionPlatformMatches(c.platform, q.platform) &&
		productionEqual(c.dbs, q.dbs) && productionEqual(c.marketType, q.marketType) && productionEqual(c.abTest, q.abTest)
}

func productionNaturalRemainder(c, q productionBenchmarkConstraint) bool {
	return productionRangeMatches(c.activity, q.activity) && productionOrderMatches(c.customerOrderCount, q.customerOrderCount) &&
		productionRangeMatches(c.slotTime, q.slotTime) && productionEqual(c.customerSegment, q.customerSegment) &&
		productionEqual(c.customerFraud, q.customerFraud) && productionEqual(c.deliveryAreaID, q.deliveryAreaID) &&
		productionEqual(c.regionID, q.regionID) && productionEqual(c.retailerUUID, q.retailerUUID) &&
		productionEqual(c.vertical, q.vertical) && productionEqual(c.slotDayOfWeek, q.slotDayOfWeek) &&
		productionEqual(c.abTest, q.abTest)
}

func productionEqual[V comparable](constraint, query *V) bool {
	return constraint == nil || query != nil && *constraint == *query
}

func productionRangeMatches(constraint, query *productionBenchmarkTimeRange) bool {
	if constraint == nil {
		return true
	}
	return query != nil && !query.since.Before(constraint.since) && !query.until.After(constraint.until)
}

func productionOrderMatches(constraint, query *productionBenchmarkOrderCount) bool {
	return constraint == nil || query != nil && constraint.total <= query.total
}

func productionPlatformMatches(constraint, query *productionBenchmarkPlatform) bool {
	if constraint == nil {
		return true
	}
	if query == nil || constraint.name != query.name {
		return false
	}
	if constraint.version == nil {
		return true
	}
	return query.version != nil && productionVersionCompare(constraint.version, query.version) <= 0
}

func productionVersionCompare(a, b *productionBenchmarkVersion) int {
	if result := cmp.Compare(a.major, b.major); result != 0 {
		return result
	}
	if result := cmp.Compare(a.minor, b.minor); result != 0 {
		return result
	}
	return cmp.Compare(a.patch, b.patch)
}

type productionRuleixMatcher struct {
	index *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID]
	local *ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID]
}

func (m *productionRuleixMatcher) Match(q productionBenchmarkConstraint, dst *[]productionBenchmarkID) {
	m.local.Search(q, dst)
}

func (m *productionRuleixMatcher) BeginRequest() {
	if m.local != nil {
		panic("Ruleix Local request already active")
	}
	m.local = m.index.Local()
}

func (m *productionRuleixMatcher) EndRequest() {
	if m.local == nil {
		panic("Ruleix Local request is not active")
	}
	m.local.Close()
	m.local = nil
}

type productionBitmapMatcher struct {
	constraints []productionBenchmarkConstraint
	ids         []productionBenchmarkID
	customers   map[[16]byte]*roaring.Bitmap
	wildcard    *roaring.Bitmap
}

func newProductionBitmapMatcher(constraints []productionBenchmarkConstraint, ids []productionBenchmarkID) *productionBitmapMatcher {
	m := &productionBitmapMatcher{constraints: constraints, ids: ids,
		customers: make(map[[16]byte]*roaring.Bitmap), wildcard: roaring.New()}
	for i, constraint := range constraints {
		if constraint.customerUUID == nil {
			m.wildcard.Add(uint32(i))
			continue
		}
		posting := m.customers[*constraint.customerUUID]
		if posting == nil {
			posting = roaring.New()
			m.customers[*constraint.customerUUID] = posting
		}
		posting.Add(uint32(i))
	}
	return m
}

// Match uses the most selective production dimension as a handwritten bitmap
// index, then applies allocation-free schema-specialized residual predicates.
func (m *productionBitmapMatcher) Match(q productionBenchmarkConstraint, dst *[]productionBenchmarkID) {
	m.appendMatches(m.wildcard, q, dst)
	if q.customerUUID != nil {
		m.appendMatches(m.customers[*q.customerUUID], q, dst)
	}
}

func (m *productionBitmapMatcher) appendMatches(posting *roaring.Bitmap, q productionBenchmarkConstraint, dst *[]productionBenchmarkID) {
	if posting == nil {
		return
	}
	it := posting.Iterator()
	for it.HasNext() {
		i := it.Next()
		if productionConstraintMatches(m.constraints[i], q, true) {
			*dst = append(*dst, m.ids[i])
		}
	}
}

func productionRequestQueries(mode string, hot bool) []productionBenchmarkConstraint {
	queries := make([]productionBenchmarkConstraint, productionRequestQueryCount)
	for i := range queries {
		common := i
		if mode == "Correlated" {
			common = (i / 100) * 17
		}
		if hot {
			common %= 8
		}
		q := productionBenchmarkQuery((i*37 + common) % 365)
		customer, store := [16]byte{}, [16]byte{}
		binary.BigEndian.PutUint64(customer[8:], uint64(common%536))
		binary.BigEndian.PutUint64(store[8:], uint64((common*7)%179))
		q.customerUUID, q.storeUUID = ptr(customer), ptr(store)
		q.platform.name = productionPlatformName(common % 34)
		q.platform.version.major = common%3 + 1
		q.slotType = ptr(uint8(common%2 + 1))
		q.dbs = ptr(common%2 == 0)
		queries[i] = q
	}
	return queries
}

func productionSortedIDs(ids []productionBenchmarkID) []productionBenchmarkID {
	result := append([]productionBenchmarkID(nil), ids...)
	sort.Slice(result, func(i, j int) bool { return string(result[i][:]) < string(result[j][:]) })
	return result
}

func productionMatcherName(size int) string { return fmt.Sprintf("%dK", size/1000) }

package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type unsupportedBatchRule struct {
	bits        *roaring.Bitmap
	searchCalls int
}

func (*unsupportedBatchRule) rule() {}
func (r *unsupportedBatchRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] {
	return r
}
func (*unsupportedBatchRule) validate(int) error { return nil }
func (*unsupportedBatchRule) insert(int, uint32) {}
func (r *unsupportedBatchRule) cardinality(int, *bitmapPool) uint64 {
	return r.bits.GetCardinality()
}
func (r *unsupportedBatchRule) estimateCheapCardinality(int) uint64 {
	return r.bits.GetCardinality()
}
func (r *unsupportedBatchRule) search(_ int, dst *roaring.Bitmap, _ *bitmapPool) {
	r.searchCalls++
	dst.Or(r.bits)
}
func (*unsupportedBatchRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*unsupportedBatchRule) collectBuildStatistics([]nodeBuildStatistics) {}

func TestExecutionCapabilityUnwrapsDirectIDOperationsTruthfully(t *testing.T) {
	leaf := &countingRule{ids: []uint32{1, 2}}
	wrapped := &inspectionDetailsRule[int]{
		child: &lossyRule[int]{child: leaf},
	}
	capability := describeExecutionCapability[int](wrapped)
	require.NotNil(t, capability.directID)
	require.True(t, capability.directID.matchesID(0, 2))

	unsupported := &inspectionDetailsRule[int]{child: &unsupportedBatchRule{bits: roaring.BitmapOf(1)}}
	require.Nil(t, describeExecutionCapability[int](unsupported).directID)
}

func TestExecutionCapabilityCompilesOuterCachedBitmapProvider(t *testing.T) {
	leaf := &cachedEstimateRule{
		costlyEstimateRule: &costlyEstimateRule{child: &countingRule{ids: []uint32{1}}},
		bits:               roaring.BitmapOf(1),
	}
	metrics := &inspectorRuntime{}
	wrapped := &inspectedRuntimeRule[int]{child: leaf, metrics: metrics}

	capability := describeExecutionCapability[int](wrapped)
	require.Equal(t, cachedBitmapProvider[int](wrapped), capability.cached)
	bits, found := capability.cached.lookupCachedBitmap(0, newLocalBitmapPool(0))
	require.True(t, found)
	require.Equal(t, []uint32{1}, bits.ToArray())
}

func TestEqualityDirectIDWorkIsBoundedButLargeCandidateSetsStayOnBitmapPath(t *testing.T) {
	equality := &eqRule[int, int]{wildcard: roaring.BitmapOf(1)}
	capability := describeExecutionCapability[int](equality)
	require.Equal(t, uint64(32), capability.directIDWork)

	rule := &allRule[int]{children: []Rule[int]{
		&matchAllRule[int]{bits: roaring.BitmapOf(1)},
		equality,
	}}
	rule.prepareSearch()
	remaining := []rankedBitmap{{card: 50_000, childIdx: 1}}
	require.True(t, rule.shouldValidateRemaining(128, remaining))
	require.False(t, rule.shouldValidateRemaining(allCheapDirectIDScanLimit+1, remaining))
}

func TestNestedAllAdvertisesDirectIDOnlyWhenEveryChildSupportsIt(t *testing.T) {
	supported := &allRule[int]{children: []Rule[int]{
		&countingRule{ids: []uint32{1}},
		&inspectionDetailsRule[int]{child: &countingRule{ids: []uint32{1}}},
	}}
	supported.prepareSearch()
	require.NotNil(t, describeExecutionCapability[int](supported).directID)

	unsupported := &allRule[int]{children: []Rule[int]{
		&countingRule{ids: []uint32{1}},
		&unsupportedBatchRule{bits: roaring.BitmapOf(1)},
	}}
	unsupported.prepareSearch()
	require.Nil(t, describeExecutionCapability[int](unsupported).directID)
}

func TestAllMaterializesUnsupportedRuleOncePerCandidateBatch(t *testing.T) {
	unsupported := &unsupportedBatchRule{bits: roaring.BitmapOf(2, 4, 6)}
	rule := &allRule[int]{children: []Rule[int]{
		&countingRule{ids: []uint32{1, 2, 3, 4, 5, 6}},
		unsupported,
	}}
	rule.prepareSearch()

	dst := roaring.New()
	rule.search(0, dst, newBitmapPool())

	require.Equal(t, []uint32{2, 4, 6}, dst.ToArray())
	require.Equal(t, 1, unsupported.searchCalls)
}

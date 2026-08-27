package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlannerProfilePublishesBatchedImmutableSnapshot(t *testing.T) {
	rule := &allRule[int]{}
	pool := newLocalBitmapPool(0)
	pool.samplePlanner = true
	pool.allPlans = map[any]*localAllPlan{
		rule: {order: []int{1, 0}},
	}
	ranked := []rankedBitmap{{childIdx: 1, card: 4}, {childIdx: 0, card: 20}}
	pool.beginPlannerObservation(rule, ranked)
	pool.finishPlannerObservation(rule, 1)

	publisher := &plannerProfilePublisher{}
	publisher.publish(pool.plannerOverlay)
	first := publisher.snapshot.Load()
	require.NotNil(t, first)
	shape := first.rules[rule].shapes[plannerShape(4)]
	require.Equal(t, uint64(1), shape.samples)
	require.Equal(t, uint64(1), shape.actualCardinality)
	require.Equal(t, uint64(4), shape.candidateChecks)
	require.Equal(t, uint64(1), shape.candidateSamples)
	require.Equal(t, uint64(3), shape.candidateRejections)
	require.Equal(t, []uint16{1, 0}, shape.order[:shape.orderLen])

	pool.plannerOverlay = plannerProfileOverlay{}
	pool.beginPlannerObservation(rule, ranked)
	pool.finishPlannerObservation(rule, 0)
	publisher.publish(pool.plannerOverlay)
	second := publisher.snapshot.Load()
	require.NotSame(t, first, second)
	require.Equal(t, uint64(1), first.rules[rule].shapes[plannerShape(4)].samples)
	require.Equal(t, uint64(2), second.rules[rule].shapes[plannerShape(4)].samples)
	require.Equal(t, uint64(1), second.rules[rule].shapes[plannerShape(4)].emptyResults)
}

func TestPlannerProfileSeedsFreshLocalWithoutQueryValues(t *testing.T) {
	rule := &allRule[int]{children: []Rule[int]{&matchAllRule[int]{}, &matchAllRule[int]{}}}
	profile := plannerRuleProfile{}
	profile.shapes[2] = plannerShapeProfile{
		samples: 7, candidateChecks: 28, candidateSamples: 7, orderLen: 2,
		order: [plannerProfileOrderLimit]uint16{1, 0},
	}
	pool := newLocalBitmapPool(0)
	pool.plannerSnapshot = &plannerProfileSnapshot{rules: map[any]plannerRuleProfile{rule: profile}}

	plan := pool.seedSharedPlannerOrder(rule, 2)

	require.Equal(t, []int{1, 0}, plan.order)
	require.Equal(t, uint64(4), plan.firstCard)
	require.Len(t, pool.plannerSnapshot.rules, 1)
}

func TestPlannerProfileRequiresConfidenceAndDistinguishesZeroCost(t *testing.T) {
	rule := &allRule[int]{children: []Rule[int]{&matchAllRule[int]{}, &matchAllRule[int]{}}}
	profile := plannerRuleProfile{}
	profile.shapes[0] = plannerShapeProfile{
		samples: plannerProfileMinSamples - 1, candidateSamples: plannerProfileMinSamples - 1,
		orderLen: 2, order: [plannerProfileOrderLimit]uint16{1, 0},
	}
	pool := newLocalBitmapPool(0)
	pool.plannerSnapshot = &plannerProfileSnapshot{rules: map[any]plannerRuleProfile{rule: profile}}
	require.Nil(t, pool.seedSharedPlannerOrder(rule, 2))

	shape := &profile.shapes[0]
	shape.samples++
	shape.candidateSamples++
	pool.plannerSnapshot = &plannerProfileSnapshot{rules: map[any]plannerRuleProfile{rule: profile}}
	plan := pool.seedSharedPlannerOrder(rule, 2)
	require.NotNil(t, plan)
	require.Zero(t, plan.firstCard)

	profile.shapes[0].candidateSamples = 0
	pool.plannerSnapshot = &plannerProfileSnapshot{rules: map[any]plannerRuleProfile{rule: profile}}
	require.Equal(t, ^uint64(0), pool.seedSharedPlannerOrder(rule, 2).firstCard)
}

func TestPlannerProfileExploresOnlyBoundedSampledLocals(t *testing.T) {
	index, err := New[[2]int, int](All(
		Include(func(value [2]int) (int, bool) { return value[0], true }),
		Include(func(value [2]int) (int, bool) { return value[1], true }),
	)).Build(Zip([][2]int{{1, 1}}, []int{1}))
	require.NoError(t, err)

	index.localTelemetry.Store(511)
	local := index.Local()
	require.True(t, local.pool.samplePlanner)
	require.True(t, local.pool.explorePlanner)
	local.Close()

	local = index.Local()
	require.False(t, local.pool.samplePlanner)
	require.False(t, local.pool.explorePlanner)
	local.Close()
}

func TestPlannerProfileUsesBoundedShapesAndOrder(t *testing.T) {
	require.Equal(t, plannerProfileShapeLimit-1, plannerShape(^uint64(0)))
	require.Less(t, plannerShape(uint64(1)<<40), plannerProfileShapeLimit)
	require.Equal(t, 16, plannerProfileOrderLimit)
	require.Equal(t, 4, plannerProfileMinSamples)
}

func TestSampledLocalPublishesPlannerProfileOnClose(t *testing.T) {
	rule := All(
		Include(func(value [2]int) (int, bool) { return value[0], true }),
		Include(func(value [2]int) (int, bool) { return value[1], true }),
	)
	index, err := New[[2]int, int](rule).Build(Zip(
		[][2]int{{1, 1}, {1, 2}, {2, 1}},
		[]int{0, 1, 2},
	))
	require.NoError(t, err)
	index.localTelemetry.Store(63)
	local := index.Local()
	require.True(t, local.pool.samplePlanner)
	var matches []int
	require.True(t, local.Search([2]int{1, 1}, &matches))
	require.Equal(t, []int{0}, matches)
	require.Nil(t, index.plannerProfiles.snapshot.Load())

	local.Close()
	snapshot := index.plannerProfiles.snapshot.Load()
	require.NotNil(t, snapshot)
	require.Len(t, snapshot.rules, 1)
}

package ruleix

import (
	"sync"
	"sync/atomic"
)

const (
	plannerProfileShapeLimit = 8
	plannerProfileOrderLimit = 16
	plannerProfileMinSamples = 4
)

type plannerProfileSnapshot struct {
	rules map[any]plannerRuleProfile
}

type plannerRuleProfile struct {
	shapes [plannerProfileShapeLimit]plannerShapeProfile
}

type plannerShapeProfile struct {
	samples             uint64
	operationCost       uint64
	actualCardinality   uint64
	emptyResults        uint64
	candidateChecks     uint64
	candidateSamples    uint64
	candidateRejections uint64
	order               [plannerProfileOrderLimit]uint16
	orderLen            uint8
}

type plannerProfileOverlay struct {
	rules map[any]*plannerRuleProfile
}

type plannerProfilePublisher struct {
	mu       sync.Mutex
	snapshot atomic.Pointer[plannerProfileSnapshot]
}

func plannerShape(cardinality uint64) int {
	if cardinality == ^uint64(0) {
		return plannerProfileShapeLimit - 1
	}
	shape := 0
	for cardinality > 1 && shape < plannerProfileShapeLimit-2 {
		cardinality = (cardinality + 1) >> 1
		shape++
	}
	return shape
}

func (p *bitmapPool) beginPlannerObservation(rule any, ranked []rankedBitmap) {
	if !p.samplePlanner || len(ranked) == 0 {
		return
	}
	plan := p.allPlans[rule]
	if plan == nil {
		return
	}
	plan.observation = localPlannerObservation{
		active:     true,
		shape:      uint8(plannerShape(ranked[0].card)),
		candidates: ranked[0].card,
	}
}

func (p *bitmapPool) finishPlannerObservation(rule any, result uint64) {
	if !p.samplePlanner {
		return
	}
	plan := p.allPlans[rule]
	if plan == nil || !plan.observation.active {
		return
	}
	observation := plan.observation
	plan.observation = localPlannerObservation{}
	if p.plannerOverlay.rules == nil {
		p.plannerOverlay.rules = make(map[any]*plannerRuleProfile)
	}
	profile := p.plannerOverlay.rules[rule]
	if profile == nil {
		profile = &plannerRuleProfile{}
		p.plannerOverlay.rules[rule] = profile
	}
	shape := &profile.shapes[observation.shape]
	shape.samples++
	shape.actualCardinality = saturatingAdd(shape.actualCardinality, result)
	if result == 0 {
		shape.emptyResults++
	}
	if observation.candidates != ^uint64(0) {
		shape.candidateSamples++
		shape.candidateChecks = saturatingAdd(shape.candidateChecks, observation.candidates)
		if result < observation.candidates {
			shape.candidateRejections = saturatingAdd(shape.candidateRejections, observation.candidates-result)
		}
	}
	// Cost is a deterministic work proxy. It deliberately avoids timers: one
	// unit for selecting the operation plus one per candidate examined.
	cost := uint64(1)
	if observation.candidates != ^uint64(0) {
		cost = saturatingAdd(cost, observation.candidates)
	}
	shape.operationCost = saturatingAdd(shape.operationCost, cost)
	shape.orderLen = uint8(min(len(plan.order), plannerProfileOrderLimit))
	for i := range int(shape.orderLen) {
		shape.order[i] = uint16(plan.order[i])
	}
}

func (p *bitmapPool) observePlannerEmpty(rule any) {
	if !p.samplePlanner {
		return
	}
	if p.plannerOverlay.rules == nil {
		p.plannerOverlay.rules = make(map[any]*plannerRuleProfile)
	}
	profile := p.plannerOverlay.rules[rule]
	if profile == nil {
		profile = &plannerRuleProfile{}
		p.plannerOverlay.rules[rule] = profile
	}
	shape := &profile.shapes[0]
	shape.samples++
	shape.emptyResults++
	shape.operationCost = saturatingAdd(shape.operationCost, 1)
}

func (p *plannerProfilePublisher) publish(overlay plannerProfileOverlay) {
	if len(overlay.rules) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := &plannerProfileSnapshot{rules: make(map[any]plannerRuleProfile, len(overlay.rules))}
	if current := p.snapshot.Load(); current != nil {
		for rule, profile := range current.rules {
			next.rules[rule] = profile
		}
	}
	for rule, delta := range overlay.rules {
		profile := next.rules[rule]
		for i := range profile.shapes {
			dst, src := &profile.shapes[i], &delta.shapes[i]
			dst.samples = saturatingAdd(dst.samples, src.samples)
			dst.operationCost = saturatingAdd(dst.operationCost, src.operationCost)
			dst.actualCardinality = saturatingAdd(dst.actualCardinality, src.actualCardinality)
			dst.emptyResults = saturatingAdd(dst.emptyResults, src.emptyResults)
			dst.candidateChecks = saturatingAdd(dst.candidateChecks, src.candidateChecks)
			dst.candidateSamples = saturatingAdd(dst.candidateSamples, src.candidateSamples)
			dst.candidateRejections = saturatingAdd(dst.candidateRejections, src.candidateRejections)
			if src.orderLen != 0 {
				dst.order, dst.orderLen = src.order, src.orderLen
			}
		}
		next.rules[rule] = profile
	}
	p.snapshot.Store(next)
}

func (p *bitmapPool) seedSharedPlannerOrder(rule any, children int) *localAllPlan {
	// One in eight sampled Locals deliberately uses the deterministic build-time
	// model. This bounded exploration prevents an early shared prior from
	// suppressing contrary evidence without adding work to ordinary Locals.
	if p.explorePlanner || p.plannerSnapshot == nil || children > plannerProfileOrderLimit {
		return nil
	}
	profile, ok := p.plannerSnapshot.rules[rule]
	if !ok {
		return nil
	}
	best := -1
	for i := range profile.shapes {
		shape := &profile.shapes[i]
		if int(shape.orderLen) != children || shape.samples < plannerProfileMinSamples {
			continue
		}
		if best < 0 || shape.samples > profile.shapes[best].samples {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	shape := &profile.shapes[best]
	firstCard := ^uint64(0)
	if shape.candidateSamples != 0 {
		firstCard = shape.candidateChecks / shape.candidateSamples
	}
	plan := &localAllPlan{order: make([]int, children), firstCard: firstCard}
	var seen [plannerProfileOrderLimit]bool
	for i := range children {
		if int(shape.order[i]) >= children {
			return nil
		}
		if seen[shape.order[i]] {
			return nil
		}
		seen[shape.order[i]] = true
		plan.order[i] = int(shape.order[i])
	}
	return plan
}

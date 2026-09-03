package ruleix

import "sync/atomic"

// Histogram groups observed values into stable, allocation-free bins. Each
// bin includes its lower bound and excludes the next bin's lower bound.
type Histogram struct {
	Zero, One, TwoToFour, FiveToSixteen, SeventeenTo256, Above256 uint64
}

type inspectorRuntime struct {
	candidateChecks, rangePrunings, emptyResults                             atomic.Uint64
	cacheHits, cacheMisses, cacheAdmissions, cacheEvictions, cacheExpansions atomic.Uint64
	cardinality                                                              [6]atomic.Uint64
}

type inspectorRuntimeSnapshot struct {
	cacheHit, cacheMiss, cacheAdmission, cacheEviction, cacheExpansion uint64
	candidateCheck, rangePruning, emptyResult                          uint64
	cardinality                                                        Histogram
}

type inspectorRuntimeValues struct {
	candidateChecks, rangePrunings, emptyResults                             uint64
	cacheHits, cacheMisses, cacheAdmissions, cacheEvictions, cacheExpansions uint64
	cardinality                                                              [6]uint64
}

type inspectorRuntimeObserver struct {
	shared *inspectorRuntime
	local  *inspectorRuntimeValues
}

func (o inspectorRuntimeObserver) candidateCheck() {
	if o.local != nil {
		o.local.candidateChecks++
		return
	}
	o.shared.candidateChecks.Add(1)
}
func (o inspectorRuntimeObserver) rangePruning() {
	if o.local != nil {
		o.local.rangePrunings++
		return
	}
	o.shared.rangePrunings.Add(1)
}
func (o inspectorRuntimeObserver) cacheHit() {
	if o.local != nil {
		o.local.cacheHits++
		return
	}
	o.shared.cacheHits.Add(1)
}
func (o inspectorRuntimeObserver) cacheMiss() {
	if o.local != nil {
		o.local.cacheMisses++
		return
	}
	o.shared.cacheMisses.Add(1)
}
func (o inspectorRuntimeObserver) cacheAdmission() {
	if o.local != nil {
		o.local.cacheAdmissions++
		return
	}
	o.shared.cacheAdmissions.Add(1)
}
func (o inspectorRuntimeObserver) cacheEviction() {
	if o.local != nil {
		o.local.cacheEvictions++
		return
	}
	o.shared.cacheEvictions.Add(1)
}
func (o inspectorRuntimeObserver) cacheExpansion() {
	if o.local != nil {
		o.local.cacheExpansions++
		return
	}
	o.shared.cacheExpansions.Add(1)
}

func (o inspectorRuntimeObserver) observeCardinality(n uint64) {
	i := 5
	switch {
	case n == 0:
		i = 0
		if o.local != nil {
			o.local.emptyResults++
		} else {
			o.shared.emptyResults.Add(1)
		}
	case n == 1:
		i = 1
	case n <= 4:
		i = 2
	case n <= 16:
		i = 3
	case n <= 256:
		i = 4
	}
	if o.local != nil {
		o.local.cardinality[i]++
	} else {
		o.shared.cardinality[i].Add(1)
	}
}

type inspector struct{ state inspectorState }

var _ Inspector = (*inspector)(nil)

func (i *inspector) inspectionState() *inspectorState { return &i.state }

type unboundInspectorSnapshot struct{}

func (unboundInspectorSnapshot) bound() bool                { return false }
func (unboundInspectorSnapshot) mode() RuleMode             { return "" }
func (unboundInspectorSnapshot) strategy() string           { return "" }
func (unboundInspectorSnapshot) entryCount() uint64         { return 0 }
func (unboundInspectorSnapshot) ruleCount() uint64          { return 0 }
func (unboundInspectorSnapshot) details() inspectionDetails { return inspectionDetails{} }

type exactInspectorSnapshot struct {
	strategyName string
	modeName     RuleMode
	entries      uint64
	rules        uint64
	detail       inspectionDetails
}

func (exactInspectorSnapshot) bound() bool { return true }
func (s exactInspectorSnapshot) mode() RuleMode {
	if s.modeName == "" {
		return RuleModeExact
	}
	return s.modeName
}
func (s exactInspectorSnapshot) strategy() string           { return s.strategyName }
func (s exactInspectorSnapshot) entryCount() uint64         { return s.entries }
func (s exactInspectorSnapshot) ruleCount() uint64          { return s.rules }
func (s exactInspectorSnapshot) details() inspectionDetails { return s.detail }

var unboundInspector = &inspectorSnapshotBox{snapshot: unboundInspectorSnapshot{}}

func (i *inspector) Snapshot() InspectorSnapshot {
	snapshot := i.state.published.Load()
	if snapshot == nil {
		snapshot = unboundInspector
	}
	b := &i.state.runtime.cardinality
	return InspectorSnapshot{build: snapshot.snapshot, runtime: inspectorRuntimeSnapshot{
		cacheHit: i.state.runtime.cacheHits.Load(), cacheMiss: i.state.runtime.cacheMisses.Load(),
		cacheAdmission: i.state.runtime.cacheAdmissions.Load(), cacheEviction: i.state.runtime.cacheEvictions.Load(),
		cacheExpansion: i.state.runtime.cacheExpansions.Load(),
		candidateCheck: i.state.runtime.candidateChecks.Load(), rangePruning: i.state.runtime.rangePrunings.Load(),
		emptyResult: i.state.runtime.emptyResults.Load(),
		cardinality: Histogram{b[0].Load(), b[1].Load(), b[2].Load(), b[3].Load(), b[4].Load(), b[5].Load()},
	}}
}

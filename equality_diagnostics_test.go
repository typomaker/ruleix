package ruleix

import "sort"

// EqualityHashForTest exposes the compiled semantic hash to external
// black-box benchmarks without adding a production API.
func EqualityHashForTest[V comparable](value V) uint64 {
	codec, err := compileEqualityCodec[V]()
	if err != nil {
		panic(err)
	}
	return codec.hash(value)
}

// EqualityDiagnostic is test-only physical-shape data for one compiled lossy
// equality leaf. It deliberately lives outside the public library build.
type EqualityDiagnostic struct {
	PhysicalKeys      uint64
	Items             uint64
	MinPosting        uint64
	MedianPosting     uint64
	P95Posting        uint64
	MaxPosting        uint64
	WeightedCollision float64
}

type equalityDiagnosticProvider interface {
	equalityDiagnostic() EqualityDiagnostic
}

func (r *eqRule[T, V, K]) equalityDiagnostic() EqualityDiagnostic {
	postings := make([]uint64, len(r.values.sets))
	var items, squared uint64
	for i := range r.values.sets {
		cardinality := r.values.sets[i].cardinality()
		postings[i] = cardinality
		items += cardinality
		squared += cardinality * cardinality
	}
	sort.Slice(postings, func(i, j int) bool { return postings[i] < postings[j] })
	diagnostic := EqualityDiagnostic{PhysicalKeys: uint64(len(postings)), Items: items}
	if len(postings) == 0 {
		return diagnostic
	}
	diagnostic.MinPosting = postings[0]
	diagnostic.MedianPosting = postings[len(postings)/2]
	diagnostic.P95Posting = postings[(len(postings)*95+99)/100-1]
	diagnostic.MaxPosting = postings[len(postings)-1]
	if items != 0 {
		diagnostic.WeightedCollision = float64(squared) / float64(items)
	}
	return diagnostic
}

// EqualityDiagnostics returns every quantized equality leaf from a test index.
func EqualityDiagnostics[C any, ID comparable](index *Index[C, ID]) []EqualityDiagnostic {
	var diagnostics []EqualityDiagnostic
	var walk func(Rule[C], inspectionDetails)
	walk = func(rule Rule[C], details inspectionDetails) {
		switch typed := rule.(type) {
		case *inspectionDetailsRule[C]:
			walk(typed.child, typed.details)
		case *inspectRule[C]:
			walk(typed.child, details)
		case *allRule[C]:
			for _, child := range typed.children {
				walk(child, details)
			}
		default:
			if provider, ok := rule.(equalityDiagnosticProvider); ok {
				diagnostics = append(diagnostics, provider.equalityDiagnostic())
			}
		}
	}
	walk(index.root, inspectionDetails{})
	return diagnostics
}

import json
import sys
from pathlib import Path

p = Path(sys.argv[1]).resolve()
base = p / "source"
A = """package ruleix

// The root owns candidate-mask scratch; a negative first index records a
// rejecting root operand while source is nil. No query identity is cached.
func (r *allRule[T]) retryEmptyOperand(pool *bitmapPool, value T) bool {
 m := pool.candidateMask
 if m == nil || pool.observeRuntime || r.executionCounters != nil || m.first >= 0 { return false }
 estimate, known := cheapCardinality(r.children[-m.first-1], value)
 if known && estimate == 0 { return true }
 m.first = 0
 return false
}

func (r *allRule[T]) rememberEmptyOperand(pool *bitmapPool, value T) {
 m := pool.candidateMask
 if m == nil || pool.observeRuntime || r.executionCounters != nil { return }
 for i, child := range r.children {
  if estimate, known := cheapCardinality(child, value); known && estimate == 0 {
   m.source, m.count, m.first = nil, 0, -i-1
   return
  }
 }
}
"""
B = """package ruleix

// Encode up to eight immutable child indices in existing All padding.
func (r *allRule[T]) prepareEmptyOrder() {
 if len(r.children) > 8 { return }
 position := uint(0)
 for pass := range 2 {
  for i, child := range r.children {
   _, cheap := unwrapExecutionRule(child).(cheapCardinalityEstimator[T])
   if cheap != (pass == 0) { continue }
   r.emptyOrder |= uint32(i+1) << (4*position)
   position++
  }
 }
}
"""
for name in ["base", "a", "b", "c", "ab", "ac", "bc", "abc"]:
    changes = {}

    def get(f, variant_changes=changes):
        return variant_changes.get(f, (base / f).read_text())

    def put(f, s, variant_changes=changes):
        variant_changes[f] = s

    if "a" in name and name != "base":
        put("local_empty_operand.go", A)
        s = get("index_search.go").replace(
            "result := *dst\n\tif len(exclusions) == 0 {",
            "result := *dst\n\tif len(exclusions) == 0 && root.retryEmptyOperand(pool, value) { return }\n\tif len(exclusions) == 0 {",
            1,
        )
        s = s.replace(
            "if !root.rankChildren(value, pool, rankedChildren) || len(rankedChildren) == 0 {",
            "if !root.rankChildren(value, pool, rankedChildren) || len(rankedChildren) == 0 {\n\t\tif len(exclusions) == 0 { root.rememberEmptyOperand(pool, value) }",
            1,
        )
        put("index_search.go", s)
    if "b" in name and name != "base":
        put("all_empty_order.go", B)
        s = get("all.go").replace(
            "compiledEqualityClasses bool",
            "compiledEqualityClasses bool\n\temptyOrder uint32",
        )
        s = s.replace(
            "r.planningPrepared = true",
            "r.prepareEmptyOrder()\n\tr.planningPrepared = true",
        )
        put("all.go", s)
        s = get("all_planning.go").replace(
            "cheapBound := ^uint64(0)\n\tfor i, child := range r.children {",
            "cheapBound := ^uint64(0)\n\torder := r.emptyOrder\n\tfor position := range r.children {\n\t\ti := position\n\t\tif order != 0 { i = int(order & 15)-1; order >>= 4 }\n\t\tchild := r.children[i]",
            1,
        )
        put("all_planning.go", s)
    if "c" in name and name != "base":
        s = get("eq.go").replace(
            "nodeID          nodeID", "nodeID          nodeID\n\twildcardFree bool"
        )
        s = s.replace(
            "func (r *eqRule[T, V, K]) prepareSearch() {",
            "func (r *eqRule[T, V, K]) prepareSearch() {\n\tr.wildcardFree = r.wildcard.IsEmpty()",
        )
        put("eq.go", s)
        s = get("quantized_equality.go").replace(
            "if !r.wildcard.IsEmpty() {",
            "if !r.wildcardFree && !r.wildcard.IsEmpty() {",
            1,
        )
        put("quantized_equality.go", s)
    dest = p / name
    dest.mkdir(exist_ok=True)
    overlay = {}
    for f, s in changes.items():
        target = dest / f
        target.write_text(s)
        overlay[str(base / f)] = str(target)
    (p / (name + ".json")).write_text(json.dumps({"Replace": overlay}))
print("Prepared eight variants")

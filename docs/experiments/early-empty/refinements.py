import json
import sys
from pathlib import Path

p = Path(sys.argv[1]).resolve()
base = p / "source"
C = """package ruleix

import "math/bits"

type emptyKeyOperand[T any] interface {
 supportsEmptyKeyCheck() bool
 isEmptyKey(T) bool
}

// This immutable empty result requires no Local query identity or allocation.
var emptyKeyResult localAllResult

func (r *eqRule[T,V,K]) supportsEmptyKeyCheck() bool {
 return r.coarsen == nil && r.wildcard.IsEmpty()
}
func (r *eqRule[T,V,K]) isEmptyKey(v T) bool {
 value,present := r.get(v)
 return !present || r.values.get(r.equalityKey(value)) == nil
}
func (r *allRule[T]) prepareEmptyKeys() {
 r.emptyKeys = 0
 if len(r.children)>8 { return }
 for i,child := range r.children {
  if operand,ok := child.(emptyKeyOperand[T]); ok && operand.supportsEmptyKeyCheck() { r.emptyKeys |= uint8(1)<<i }
 }
}
func (r *allRule[T]) loadEmptyKeyResult(pool *bitmapPool, v T) *localAllResult {
 for pending:=r.emptyKeys; pending!=0; pending&=pending-1 {
  i:=bits.TrailingZeros8(pending)
  if r.children[i].(emptyKeyOperand[T]).isEmptyKey(v) {
   TRACK_REJECTION
   return &emptyKeyResult
  }
 }
 return nil
}
"""
for name in ["a2", "c2", "ab", "ac", "bc", "abc"]:
    useA = name in ["a2", "ab", "ac", "abc"]
    useB = name in ["ab", "bc", "abc"]
    useC = name in ["c2", "ac", "bc", "abc"]
    changes = {}
    if useA:
        for f in ["index_search.go", "local_empty_operand.go"]:
            changes[f] = (p / "a" / f).read_text()
        changes["index_search.go"] = changes["index_search.go"].replace(
            "len(exclusions) == 0 && root.retryEmptyOperand(pool, value)",
            "len(exclusions) == 0 && pool.candidateMask != nil && pool.candidateMask.first < 0 && root.retryEmptyOperand(pool, value)",
        )
        # A retained nonnil source proves this immutable operand supports exact masks;
        # a later nil posting is an empty key, not a reason to discard the old source.
        s = (base / "local_candidate_mask.go").read_text()
        s = s.replace(
            "if posting != m.source {",
            "if posting == nil { return true }\n\tif posting != m.source {",
        )
        changes["local_candidate_mask.go"] = s
    if useB:
        for f in ["all.go", "all_planning.go", "all_empty_order.go"]:
            changes[f] = (p / "b" / f).read_text()
        changes["all_empty_order.go"] = changes["all_empty_order.go"].replace(
            "if len(r.children) > 8 { return }",
            "r.emptyOrder = 0\n if len(r.children) > 8 { return }",
        )
    if useC:
        s = changes.get("all.go", (base / "all.go").read_text())
        s = s.replace(
            "maskEqualityOrder       uint8",
            "maskEqualityOrder       uint8\n\temptyKeys uint8",
        )
        s = s.replace(
            "r.planningPrepared = true",
            "r.prepareEmptyKeys()\n\tr.planningPrepared = true",
        )
        changes["all.go"] = s
        s = changes.get("all_planning.go", (base / "all_planning.go").read_text())
        old = "func (r *allRule[T]) loadLocalQueryResult(pool *bitmapPool, value T) *localAllResult {\n\tif pool.local == nil || pool.observeRuntime || pool.allPlans == nil {\n\t\treturn nil\n\t}"
        new = """func (r *allRule[T]) loadLocalQueryResult(pool *bitmapPool, value T) *localAllResult {
 if pool.local == nil || pool.observeRuntime { return nil }
 if pool.allPlans == nil {
  if r.emptyKeys == 0 { return nil }
  return r.loadEmptyKeyResult(pool,value)
 }"""
        assert old in s
        changes["all_planning.go"] = s.replace(old, new)
        track = (
            "m:=pool.candidateMask; m.source,m.count,m.first=nil,0,-i-1" if useA else ""
        )
        changes["local_empty_keys.go"] = C.replace("TRACK_REJECTION", track)
    dest = p / name
    dest.mkdir(exist_ok=True)
    for f, s in changes.items():
        (dest / f).write_text(s)
    (p / (name + ".json")).write_text(
        json.dumps({"Replace": {str(base / f): str(dest / f) for f in changes}})
    )
print("Prepared refinements and combinations")

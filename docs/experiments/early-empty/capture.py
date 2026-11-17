import json
import sys
from pathlib import Path

p = Path(sys.argv[1]).resolve()
base = p / "source"
for name, previous in [("a4", "a3"), ("a4b", "a3b")]:
    changes = {f.name: f.read_text() for f in (p / previous).glob("*.go")}
    s = changes.get("all_planning.go", (base / "all_planning.go").read_text())
    start = s.index("//nolint:gocognit // Ranking")
    end = s.index("//nolint:nestif // The exact fast path")
    core = s[start:end]
    core = core.replace(
        "func (r *allRule[T]) rankChildren(",
        "func (r *allRule[T]) rankChildrenRememberEmpty(",
        1,
    )
    core = core.replace(
        "rankedChildren []rankedBitmap,\n) bool",
        "rankedChildren []rankedBitmap,\n rejected *localCandidateMask,\n) bool",
        1,
    )
    # Preserve the zero operand while its identity is already known. No rescan.
    head, tail = core.split("//nolint:nestif // Cache-aware", 1)
    head = head.replace(
        "return false",
        "if rejected != nil { rejected.first = -i-1 }\n\t\t\t\treturn false",
    )
    # The early return from reuse is before i is introduced; restore it and
    # retain the slow one-time discovery only when an existing plan rejected.
    head = head.replace(
        "return result",
        "if !result && rejected != nil { r.rememberEmptyOperand(pool,v) }\n return result",
        1,
    )
    core = head + "//nolint:nestif // Cache-aware" + tail
    wrapper = """func (r *allRule[T]) rankChildren(v T, pool *bitmapPool, ranked []rankedBitmap) bool {
 return r.rankChildrenRememberEmpty(v,pool,ranked,nil)
}

"""
    changes["all_planning.go"] = s[:start] + wrapper + s[end:]
    changes["all_empty_ranking.go"] = (
        'package ruleix\nimport "github.com/RoaringBitmap/roaring/v2"\n\n' + core
    )
    s = changes["index_search.go"]
    old = "if !root.rankChildren(value, pool, rankedChildren) || len(rankedChildren) == 0 {\n\t\tif len(exclusions) == 0 { root.rememberEmptyOperand(pool, value) }"
    assert old in s
    new = """var rejected *localCandidateMask
 if len(exclusions)==0 && !pool.observeRuntime && root.executionCounters==nil { rejected=pool.candidateMask }
 if !root.rankChildrenRememberEmpty(value, pool, rankedChildren,rejected) || len(rankedChildren) == 0 {"""
    changes["index_search.go"] = s.replace(old, new)
    # Unknown cheap bounds may still have an exact planning bitmap or checker.
    s = changes["local_empty_operand.go"]
    s = s.replace(
        "if known && estimate == 0 { return true }",
        """if known && estimate == 0 { return true }
 if !known {
  if cheapCardinalityIsZero(r.children[i],value) { return true }
  if posting,found:=r.lookupPlanningBitmap(i,r.children[i],value); found && posting.IsEmpty() { return true }
 }""",
    )
    changes["local_empty_operand.go"] = s
    dest = p / name
    dest.mkdir(exist_ok=True)
    for f, s in changes.items():
        (dest / f).write_text(s)
    (p / (name + ".json")).write_text(
        json.dumps({"Replace": {str(base / f): str(dest / f) for f in changes}})
    )

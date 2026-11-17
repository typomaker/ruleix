import json
import sys
from pathlib import Path

p = Path(sys.argv[1]).resolve()
base = p / "source"
for name, previous, useB in [("a5", "a3", False), ("a5b", "a3b", True)]:
    changes = {f.name: f.read_text() for f in (p / previous).glob("*.go")}
    s = (
        (p / "b" / "all_planning.go").read_text()
        if useB
        else (base / "all_planning.go").read_text()
    )
    start = s.index("//nolint:gocognit // Ranking")
    end = s.index("//nolint:nestif // The exact fast path")
    core = s[start:end]
    head, tail = core.split("//nolint:nestif // Cache-aware", 1)
    head = head.replace(
        "return false", "rememberEmptyRank(pool,i)\n\t\t\t\treturn false"
    )
    core = head + "//nolint:nestif // Cache-aware" + tail
    changes["all_empty_ranking.go"] = (
        'package ruleix\nimport "github.com/RoaringBitmap/roaring/v2"\n\n' + core
    )
    s = s[:start] + s[end:]
    s = s.replace(
        "if ranked.card == 0 {\n\t\t\t\t\t\treturn false, true",
        "if ranked.card == 0 {\n\t\t\t\t\t\trememberEmptyRank(pool,childIdx)\n\t\t\t\t\t\treturn false, true",
    )
    s = s.replace(
        "if !ok || estimate == 0 {\n\t\t\treturn estimate != 0, ok",
        "if !ok || estimate == 0 {\n\t\t\tif ok { rememberEmptyRank(pool,rankedChildren[0].childIdx) }\n\t\t\treturn estimate != 0, ok",
    )
    s = s.replace(
        "if ranked.card == 0 {\n\t\t\treturn false",
        "if ranked.card == 0 {\n\t\t\trememberEmptyRank(pool,childIdx)\n\t\t\treturn false",
    )
    changes["all_planning.go"] = s
    s = changes["index_search.go"]
    old = "if !root.rankChildren(value, pool, rankedChildren) || len(rankedChildren) == 0 {\n\t\tif len(exclusions) == 0 { root.rememberEmptyOperand(pool, value) }"
    new = """if len(exclusions)==0 && !pool.observeRuntime && root.executionCounters==nil && pool.candidateMask!=nil { pool.candidateMask.first=emptyRankInProgress }
 ranked := root.rankChildren(value,pool,rankedChildren)
 if m:=pool.candidateMask; m!=nil && m.first==emptyRankInProgress { m.first=0 }
 if !ranked || len(rankedChildren)==0 {"""
    assert old in s
    changes["index_search.go"] = s.replace(old, new)
    s = changes["local_empty_operand.go"]
    s = s[: s.index("func (r *allRule[T]) rememberEmptyOperand")]
    s = s.replace("m.first >= 0 {", "m.first >= 0 || m.first == emptyRankInProgress {")
    s = s.replace(
        "if known && estimate == 0 { return true }",
        """if known && estimate == 0 { return true }
 if !known {
  if cheapCardinalityIsZero(r.children[i],value) { return true }
  if posting,found:=r.lookupPlanningBitmap(i,r.children[i],value); found && posting.IsEmpty() { return true }
 }""",
    )
    s += """
// This sentinel cannot encode a valid child index on any supported int width.
const emptyRankInProgress = -int(^uint(0)>>1)-1

// Only the root arms this marker, and ranking estimates do not recurse into
// child search. Clear it before materializing or validating any child.
func rememberEmptyRank(pool *bitmapPool, child int) {
 if m:=pool.candidateMask; m!=nil && m.first==emptyRankInProgress { m.first=-child-1 }
}
"""
    changes["local_empty_operand.go"] = s
    dest = p / name
    dest.mkdir(exist_ok=True)
    for f, s in changes.items():
        (dest / f).write_text(s)
    (p / (name + ".json")).write_text(
        json.dumps({"Replace": {str(base / f): str(dest / f) for f in changes}})
    )

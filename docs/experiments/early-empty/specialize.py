import json
import sys
from pathlib import Path

p = Path(sys.argv[1]).resolve()
base = p / "source"
for name, useB in [("a3", False), ("a3b", True)]:
    changes = {
        f: (p / "a2" / f).read_text()
        for f in [
            "index_search.go",
            "local_candidate_mask.go",
            "local_empty_operand.go",
        ]
    }
    changes["local_empty_operand.go"] = changes["local_empty_operand.go"].replace(
        "estimate, known := cheapCardinality(r.children[-m.first-1], value)",
        """i := -m.first-1
 if r.emptyKeys & (uint8(1)<<uint(i)) != 0 {
  if r.children[i].(emptyKeyOperand[T]).isEmptyKey(value) { return true }
  m.first=0
  return false
 }
 estimate, known := cheapCardinality(r.children[i], value)""",
    )
    s = (p / "c2" / "all.go").read_text()
    if useB:
        s = s.replace(
            "compiledEqualityClasses bool",
            "compiledEqualityClasses bool\n\temptyOrder uint32",
        ).replace(
            "r.prepareEmptyKeys()", "r.prepareEmptyOrder()\n\tr.prepareEmptyKeys()"
        )
        changes["all_empty_order.go"] = (p / "ab" / "all_empty_order.go").read_text()
        changes["all_planning.go"] = (p / "b" / "all_planning.go").read_text()
    changes["all.go"] = s
    helper = (p / "c2" / "local_empty_keys.go").read_text()
    start = helper.index("func (r *allRule[T]) loadEmptyKeyResult")
    helper = (
        helper[:start]
        .replace('import "math/bits"\n', "")
        .replace(
            "// This immutable empty result requires no Local query identity or allocation.\nvar emptyKeyResult localAllResult\n",
            "",
        )
    )
    changes["local_empty_keys.go"] = helper
    dest = p / name
    dest.mkdir(exist_ok=True)
    for f, s in changes.items():
        (dest / f).write_text(s)
    (p / (name + ".json")).write_text(
        json.dumps({"Replace": {str(base / f): str(dest / f) for f in changes}})
    )

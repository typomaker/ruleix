"""Count covered statements in coverage blocks intersecting changed Go lines.

Run before the release commit: python3 docs/experiments/release-0.9.0/diff-coverage.py
/tmp/ruleix-v090-final.cover. The baseline is the documented pre-release HEAD.
"""

import json
import re
import subprocess
import sys
from pathlib import Path

ranges = {}
for line in subprocess.check_output(
    ["git", "diff", "453f87d", "--unified=0", "--", "*.go"], text=True
).splitlines():
    if line.startswith("+++ b/"):
        name = line[6:]
        ranges.setdefault(name, set())
    elif line.startswith("@@"):
        match = re.search(r"\+(\d+)(?:,(\d+))?", line)
        start, count = int(match[1]), int(match[2] or 1)
        ranges[name].update(range(start, start + count))

blocks = []
for line in Path(sys.argv[1]).read_text().splitlines()[1:]:
    location, statements, count = line.split()
    name, span = location.split(":")
    name = name.removeprefix("github.com/typomaker/ruleix/")
    start, end = [int(value.split(".")[0]) for value in span.split(",")]
    if not name.endswith("_test.go") and any(
        row in ranges.get(name, set()) for row in range(start, end + 1)
    ):
        blocks.append((name, start, end, int(statements), int(count)))

total = sum(block[3] for block in blocks)
covered = sum(block[3] for block in blocks if block[4])
print(json.dumps({"covered": covered, "total": total, "blocks": blocks}, indent=2))
assert total and covered / total >= 0.9

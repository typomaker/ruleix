"""Compare operation sequences; relocation/dictionary offsets are not operands.

Usage: python3 assembly.py SCRATCH_DIRECTORY
This does not assert byte-identical binaries or prove latency equivalence.
"""
import json
from pathlib import Path
import re
import subprocess
import sys

out = Path(sys.argv[1])
report = {}
for pattern in ["searchBatch$", "batchContext$", "searchBatchChunk$",
                "batchLocalSearch", "searchBatchGroup$", "context-release"]:
    sequences = []
    for variant in ("baseline", "candidate"):
        selected = ("Local.*\\.Close$" if variant == "baseline" else "localContext.*\\.close$") if pattern == "context-release" else pattern
        code = subprocess.check_output(["go", "tool", "objdump", "-s", selected,
                                        str(out / (variant + ".test"))], text=True)
        operations = []
        for line in code.splitlines():
            match = re.match(r"\s+\S+:\d+\s+0x\w+\s+\w+\s+(.*)", line)
            if match:
                operations.append(match[1].split()[0])
        sequences.append(operations)
    assert sequences[0] and sequences[0] == sequences[1], pattern
    report[pattern] = {"instructions": len(sequences[0]), "same_operation_sequence": True}
(out / "assembly-summary.json").write_text(json.dumps(report, indent=2))
print(json.dumps(report, indent=2))

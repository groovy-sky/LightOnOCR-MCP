#!/usr/bin/env python3
import argparse
import json
import re
import unicodedata
from pathlib import Path


def normalize(text: str) -> str:
    return " ".join(unicodedata.normalize("NFKC", text).split()).casefold()


def distance(left: str, right: str) -> int:
    previous = list(range(len(right) + 1))
    for index, left_char in enumerate(left, 1):
        current = [index]
        for offset, right_char in enumerate(right, 1):
            current.append(min(current[-1] + 1, previous[offset] + 1, previous[offset - 1] + (left_char != right_char)))
        previous = current
    return previous[-1]


def structures(text: str) -> set[str]:
    found = set()
    if re.search(r"(?m)^#{1,6}\s", text): found.add("heading")
    if re.search(r"(?m)^\s*\|.+\|\s*$", text): found.add("table")
    if re.search(r"\$[^$]+\$|\\\[|\\begin\{", text): found.add("formula")
    return found


parser = argparse.ArgumentParser()
parser.add_argument("reference")
parser.add_argument("candidate")
parser.add_argument("--max-cer", type=float, required=True)
parser.add_argument("--output")
args = parser.parse_args()
reference = json.loads(Path(args.reference).read_text())
candidate = json.loads(Path(args.candidate).read_text())
if reference.keys() != candidate.keys():
    raise SystemExit("fixture sets differ")

failed = False
results = {}
for name, expected in reference.items():
    actual = candidate[name]
    expected_normalized = normalize(expected)
    cer = distance(expected_normalized, normalize(actual)) / max(1, len(expected_normalized))
    missing = structures(expected) - structures(actual)
    print(f"{name}: CER={cer:.4%} missing_structures={sorted(missing)}")
    results[name] = {"cer": cer, "missing_structures": sorted(missing)}
    failed = failed or cer > args.max_cer or bool(missing)
if args.output:
    Path(args.output).write_text(json.dumps({
        "max_cer": args.max_cer,
        "passed": not failed,
        "fixtures": results,
    }, indent=2) + "\n")
if failed:
    raise SystemExit("parity threshold exceeded")
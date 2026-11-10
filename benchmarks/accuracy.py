#!/usr/bin/env python3
"""Compare codive's Go caller data with gopls, the official Go language server.

Usage: accuracy.py <repo-with-.codive-index> [--sample 40] [--seed 1] [--show 10]

For a seeded random sample of functions and methods, this compares the callers
codive reports with the enclosing functions of the references gopls reports.

Only functions and methods are sampled, not types or constants. References
outside any function are ignored, and codive also lists interface implementers
marked as inferred, which gopls may report differently, so review the printed
examples before quoting a number. Interface dispatch is not expanded by gopls
references to a concrete method in the same way, so differences there need
reading too.
"""
import argparse
import bisect
import collections
import os
import random
import re
import sqlite3
import subprocess
import sys

MIN_CONFIDENCE = 0.5
REF_RE = re.compile(r"^(.*):(\d+):\d+(?:-\d+)?$")


def in_examples(path):
    """gopls does not load underscore directories, so they cannot be compared."""
    return any(part.startswith("_") for part in path.split("/")[:-1])


def load(db_path):
    db = sqlite3.connect(db_path)
    funcs = db.execute(
        "SELECT id, label, file_path, line_number FROM graph_nodes"
        " WHERE kind IN ('function', 'method', 'test') AND line_number > 0"
    ).fetchall()
    callers = collections.defaultdict(set)
    for src, dst, conf in db.execute("SELECT source_id, target_id, confidence FROM graph_edges WHERE kind IN ('CALLS', 'REFERENCES')"):
        if conf >= MIN_CONFIDENCE:
            callers[dst].add(src)
    return funcs, callers


def enclosing_index(funcs):
    by_file = collections.defaultdict(list)
    for fid, _, path, line in funcs:
        by_file[path].append((line, fid))
    for entries in by_file.values():
        entries.sort()
    return by_file


def enclosing(by_file, path, line):
    entries = by_file.get(path, [])
    i = bisect.bisect_right(entries, (line, "￿")) - 1
    return entries[i][1] if i >= 0 else None


def gopls_callers(repo, path, line, name, by_file, gopls):
    abs_path = os.path.join(repo, path)
    with open(abs_path, encoding="utf8", errors="replace") as f:
        text = f.read().splitlines()[line - 1]
    match = re.search(r"\b" + re.escape(name) + r"\s*[\[(]", text)
    if not match:
        return None
    pos = f"{abs_path}:{line}:{match.start() + 1}"
    run = subprocess.run([gopls, "references", pos], cwd=repo, capture_output=True, text=True, timeout=180)
    if run.returncode != 0:
        return None
    found = set()
    for out in run.stdout.splitlines():
        m = REF_RE.match(out.strip())
        if not m:
            continue
        rel = os.path.relpath(m.group(1), repo).replace("\\", "/")
        fid = enclosing(by_file, rel, int(m.group(2)))
        if fid:
            found.add(fid)
    return found


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("repo")
    ap.add_argument("--sample", type=int, default=40)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--show", type=int, default=10)
    ap.add_argument("--gopls", default="gopls")
    args = ap.parse_args()

    repo = os.path.abspath(args.repo)
    funcs, callers = load(os.path.join(repo, ".codive", "index.db"))
    by_file = enclosing_index(funcs)
    candidates = sorted(f for f in funcs if f[2].endswith(".go") and not in_examples(f[2]))
    random.Random(args.seed).shuffle(candidates)

    agree = extra = missed = checked = failed = 0
    examples = []
    for fid, name, path, line in candidates[: args.sample]:
        try:
            truth = gopls_callers(repo, path, line, name, by_file, args.gopls)
        except (OSError, subprocess.TimeoutExpired):
            truth = None
        if truth is None:
            failed += 1
            continue
        truth.discard(fid)
        ours = {c for c in callers.get(fid, set()) - {fid} if not in_examples(c.split(":")[1])}
        checked += 1
        agree += len(ours & truth)
        extra += len(ours - truth)
        missed += len(truth - ours)
        for x in sorted(ours - truth):
            examples.append(("codive only", fid, x))
        for x in sorted(truth - ours):
            examples.append(("gopls only", fid, x))

    if not checked:
        print(f"{os.path.basename(repo)}: no functions could be checked ({failed} gopls lookups failed)", file=sys.stderr)
        return 1

    precision = agree / (agree + extra) if agree + extra else 1.0
    recall = agree / (agree + missed) if agree + missed else 1.0
    print(f"{os.path.basename(repo)}: checked={checked} failed={failed} agree={agree} codive_only={extra} gopls_only={missed} "
          f"precision={precision:.3f} recall={recall:.3f}")
    random.Random(args.seed).shuffle(examples)
    for kind, target, caller in examples[: args.show]:
        print(f"  {kind}: {target} <- {caller}")


if __name__ == "__main__":
    sys.exit(main())

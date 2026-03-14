#!/usr/bin/env python3

from __future__ import annotations

import argparse
import csv
import datetime as dt
import os
import random
import re
import shlex
import statistics
import subprocess
import sys
from dataclasses import dataclass, asdict
from pathlib import Path


ROW_RE = re.compile(
    r"^design_en\s+(?P<step>\d+)\s+(?P<length>\d+)\s+(?P<keep>\d+)\s+(?P<ac>\d+)\s+"
    r"(?P<wer>[\d.]+)%\s+(?P<enc_ms>[\d.]+)\s+(?P<headroom_ms>-?[\d.]+)\s+"
    r"(?P<stop>\w+)\s+(?P<time_s>[\d.]+)s",
    re.M,
)


@dataclass
class RunResult:
    stage: str
    repeat: int
    model: str
    corpus: str
    step: int
    length: int
    keep: int
    ac: int
    wer: float
    enc_ms: float
    headroom_ms: float
    stop: str
    time_s: float
    dropping_audio: bool
    raw_log: str


def parse_ints(raw: str) -> list[int]:
    return [int(part.strip()) for part in raw.split(",") if part.strip()]


def median(values: list[float]) -> float:
    return statistics.median(values) if values else 0.0


def append(path: Path, text: str) -> None:
    with path.open("a", encoding="utf-8") as f:
        f.write(text)


def run_bench(
    args: argparse.Namespace,
    session_dir: Path,
    stage: str,
    repeat: int,
    step: int,
    length: int,
    keep: int,
    ac: int,
) -> RunResult:
    raw_dir = session_dir / "raw"
    raw_dir.mkdir(parents=True, exist_ok=True)
    tag = f"{stage}-r{repeat}-s{step}-l{length}-k{keep}-ac{ac}"
    cmd = [
        args.bench,
        "--model",
        args.model,
        "--corpus",
        args.corpus,
        "--dictate",
        args.dictate,
        "--lang",
        args.lang,
        "--steps",
        str(step),
        "--lengths",
        str(length),
        "--keeps",
        str(keep),
        "--acs",
        str(ac),
    ]
    append(
        session_dir / "diary.log",
        f"\n[{dt.datetime.now().isoformat(timespec='seconds')}] START {tag}\n",
    )
    append(session_dir / "diary.log", f"cmd: {' '.join(shlex.quote(x) for x in cmd)}\n")
    proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    combined = proc.stdout
    if proc.stderr:
        combined += (
            "\n" if combined and not combined.endswith("\n") else ""
        ) + proc.stderr
    raw_path = raw_dir / f"{tag}.log"
    raw_path.write_text(combined, encoding="utf-8")

    match = ROW_RE.search(combined)
    if not match:
        raise RuntimeError(f"could not parse benchmark row from {raw_path}")

    dropping = "dropping audio" in combined.lower()
    result = RunResult(
        stage=stage,
        repeat=repeat,
        model=args.model,
        corpus=args.corpus,
        step=int(match.group("step")),
        length=int(match.group("length")),
        keep=int(match.group("keep")),
        ac=int(match.group("ac")),
        wer=float(match.group("wer")),
        enc_ms=float(match.group("enc_ms")),
        headroom_ms=float(match.group("headroom_ms")),
        stop=match.group("stop"),
        time_s=float(match.group("time_s")),
        dropping_audio=dropping,
        raw_log=str(raw_path),
    )

    append(
        session_dir / "diary.log",
        f"RESULT {tag}: wer={result.wer:.1f}% enc_ms={result.enc_ms:.1f} headroom_ms={result.headroom_ms:.1f} stop={result.stop} dropping_audio={result.dropping_audio}\n",
    )
    return result


def write_rows(path: Path, rows: list[RunResult]) -> None:
    with path.open("w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(
            f, fieldnames=list(asdict(rows[0]).keys()), delimiter="\t"
        )
        writer.writeheader()
        for row in rows:
            writer.writerow(asdict(row))


def summarize_by_ac(rows: list[RunResult]) -> list[dict[str, float | int]]:
    groups: dict[int, list[RunResult]] = {}
    for row in rows:
        groups.setdefault(row.ac, []).append(row)

    summary = []
    for ac, items in sorted(groups.items()):
        summary.append(
            {
                "ac": ac,
                "runs": len(items),
                "median_wer": median([r.wer for r in items]),
                "median_enc_ms": median([r.enc_ms for r in items]),
                "median_headroom_ms": median([r.headroom_ms for r in items]),
                "kill_count": sum(1 for r in items if r.stop == "kill"),
                "drop_count": sum(1 for r in items if r.dropping_audio),
            }
        )
    summary.sort(
        key=lambda s: (
            s["kill_count"],
            s["drop_count"],
            s["median_wer"],
            -s["median_headroom_ms"],
        )
    )
    return summary


def summarize_configs(rows: list[RunResult]) -> list[dict[str, float | int]]:
    groups: dict[tuple[int, int, int, int], list[RunResult]] = {}
    for row in rows:
        groups.setdefault((row.step, row.length, row.keep, row.ac), []).append(row)

    summary = []
    for (step, length, keep, ac), items in groups.items():
        summary.append(
            {
                "step": step,
                "length": length,
                "keep": keep,
                "ac": ac,
                "runs": len(items),
                "median_wer": median([r.wer for r in items]),
                "median_enc_ms": median([r.enc_ms for r in items]),
                "median_headroom_ms": median([r.headroom_ms for r in items]),
                "kill_count": sum(1 for r in items if r.stop == "kill"),
                "drop_count": sum(1 for r in items if r.dropping_audio),
            }
        )
    summary.sort(
        key=lambda s: (
            s["kill_count"],
            s["drop_count"],
            s["median_wer"],
            -s["median_headroom_ms"],
            s["step"],
        )
    )
    return summary


def write_summary(path: Path, title: str, rows: list[dict[str, float | int]]) -> None:
    with path.open("w", encoding="utf-8") as f:
        f.write(f"# {title}\n\n")
        if not rows:
            f.write("No rows.\n")
            return
        headers = list(rows[0].keys())
        f.write("| " + " | ".join(headers) + " |\n")
        f.write("|" + "|".join(["---"] * len(headers)) + "|\n")
        for row in rows:
            f.write("| " + " | ".join(str(row[h]) for h in headers) + " |\n")


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run an overnight GPU sweep for dictate benchmark configs"
    )
    parser.add_argument("--bench", default="bin/bench")
    parser.add_argument("--dictate", default="bin/dictate")
    parser.add_argument("--model", default="models/ggml-large-v3-turbo-q5_0.bin")
    parser.add_argument("--corpus", default="bench/corpus")
    parser.add_argument("--lang", default="en")
    parser.add_argument("--output-dir", default="bench/results")
    parser.add_argument("--session-name", default="")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--top-ac", type=int, default=2)
    parser.add_argument("--baseline-step", type=int, default=2500)
    parser.add_argument("--baseline-length", type=int, default=5000)
    parser.add_argument("--baseline-keep", type=int, default=0)
    parser.add_argument("--ac-values", default="0,512,768,1024,1280,1500")
    parser.add_argument("--step-values", default="2000,2250,2500,2750,3000")
    parser.add_argument("--length-multipliers", default="2,3")
    parser.add_argument("--keep-values", default="0,100,200")
    parser.add_argument("--seed", type=int, default=14)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    timestamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    session_name = args.session_name or f"overnight-{timestamp}"
    session_dir = Path(args.output_dir) / session_name
    session_dir.mkdir(parents=True, exist_ok=True)

    ac_values = parse_ints(args.ac_values)
    step_values = parse_ints(args.step_values)
    keep_values = parse_ints(args.keep_values)
    length_multipliers = parse_ints(args.length_multipliers)

    stage1_plan = [
        (
            "stage1-ac",
            repeat,
            args.baseline_step,
            args.baseline_length,
            args.baseline_keep,
            ac,
        )
        for repeat in range(1, args.repeats + 1)
        for ac in ac_values
    ]
    random.Random(args.seed).shuffle(stage1_plan)

    diary = session_dir / "diary.log"
    diary.write_text(
        "Overnight GPU sweep\n\n"
        f"model: {args.model}\n"
        f"dictate: {args.dictate}\n"
        f"corpus: {args.corpus}\n"
        f"repeats: {args.repeats}\n"
        f"seed: {args.seed}\n"
        f"baseline: step={args.baseline_step} length={args.baseline_length} keep={args.baseline_keep}\n"
        f"ac-values: {ac_values}\n"
        f"step-values: {step_values}\n"
        f"length-multipliers: {length_multipliers}\n"
        f"keep-values: {keep_values}\n\n",
        encoding="utf-8",
    )

    if args.dry_run:
        append(diary, "Dry run only. Planned stage 1 runs:\n")
        for stage, repeat, step, length, keep, ac in stage1_plan:
            append(
                diary,
                f"- {stage} repeat={repeat} step={step} length={length} keep={keep} ac={ac}\n",
            )
        print(session_dir)
        return 0

    rows: list[RunResult] = []
    for stage, repeat, step, length, keep, ac in stage1_plan:
        rows.append(run_bench(args, session_dir, stage, repeat, step, length, keep, ac))
        write_rows(session_dir / "rows.tsv", rows)

    stage1_rows = [r for r in rows if r.stage == "stage1-ac"]
    ac_summary = summarize_by_ac(stage1_rows)
    write_summary(
        session_dir / "stage1-ac-summary.md", "Stage 1 AC Summary", ac_summary
    )

    selected_acs = [int(row["ac"]) for row in ac_summary[: args.top_ac]]
    append(diary, f"\nSelected AC values for stage 2: {selected_acs}\n")

    stage2_plan = []
    for repeat in range(1, args.repeats + 1):
        for ac in selected_acs:
            for step in step_values:
                for mult in length_multipliers:
                    length = step * mult
                    for keep in keep_values:
                        stage2_plan.append(
                            ("stage2-grid", repeat, step, length, keep, ac)
                        )
    random.Random(args.seed + 1).shuffle(stage2_plan)

    for stage, repeat, step, length, keep, ac in stage2_plan:
        rows.append(run_bench(args, session_dir, stage, repeat, step, length, keep, ac))
        write_rows(session_dir / "rows.tsv", rows)

    stage2_rows = [r for r in rows if r.stage == "stage2-grid"]
    config_summary = summarize_configs(stage2_rows)
    write_summary(
        session_dir / "stage2-config-summary.md",
        "Stage 2 Config Summary",
        config_summary,
    )

    finalists = [
        row
        for row in config_summary
        if row["kill_count"] == 0
        and row["drop_count"] == 0
        and row["median_headroom_ms"] >= 700
    ]
    write_summary(session_dir / "finalists.md", "Finalists", finalists[:10])
    append(diary, "\nSweep complete.\n")
    print(session_dir)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

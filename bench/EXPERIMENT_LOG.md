# Experiment Log

## Goals

- Find the safest fast default config for this laptop using the real production audio path.
- Re-validate whether CPU inference is viable for `ggml-large-v3-turbo-q5_0.bin` and a practical medium model variant.
- Understand `step`, `length`, `keep`, and `ac` well enough to tune intentionally instead of by folklore.
- Leave behind a repeatable benchmark workflow and a scientist-style diary of what was tried and why.

## Current State

- Production-path benchmark harness exists in `cmd/bench` and is committed.
- Benchmark path is verified end to end: virtual PipeWire source -> `whisper-stream` -> `dictate` -> scored hypothesis.
- Boundary duplication was reproduced at roughly step cadence and overlap trimming was added in `whisper/process.go`.
- Bench now reports `WER`, `enc-ms`, `headroom`, and stop mode.
- `ggml-medium.bin` has been downloaded to `models/ggml-medium.bin`.
- App-direction shift: typed output into a focused input box is now the next product goal, not more benchmark-only work.

## Known Findings So Far

- Best current balance on this laptop is turbo q5 on GPU at `step=2500 length=5000 keep=0 ac=1280`.
- Lower `step` can improve WER but eats safety margin quickly.
- `ac=768` appears to improve encode time materially but has hurt accuracy on the current English corpus.
- CPU turbo on this laptop previously looked too slow, but that conclusion needs a more evidence-rich re-check.

## Known Problems / Caveats

- `keep=0` is not currently trustworthy: the Go wrapper defaults zero back to `200ms`.
- Current `whisper.cpp` Docker build is generic `Release`; it does not yet prove best-possible CPU performance.
- Current corpus coverage is too narrow to claim a globally best config.

## Next Actions

1. Fix `keep=0` semantics and print effective streaming args.
2. Audit build flags and add an optimized native CPU build path if it is simple and justified.
3. Re-test turbo q5 on CPU with strong evidence (`enc-ms`, `headroom`, drop behavior, WER).
4. Quantize medium to q5 if practical and benchmark it on CPU.
5. Prepare and launch an overnight GPU sweep once semantics are fixed.

## Overnight Sweep Summary

- Overnight session completed successfully in `bench/results/overnight-20260314-gpu-sweep`.
- Stage 1 selected `ac=1280` and `ac=1500` as the best accuracy/perf tradeoffs on the baseline `3000/8000/200` config.
- Stage 2 clear winners were all in the `2250-3000ms` step range; no run reported dropped audio.

### Best Accuracy

- `step=2250 length=6750 keep=100 ac=1500`
  - median `WER 9.0%`
  - median `enc 1293.9ms`
  - median headroom `956.1ms`

### Nearly-As-Good With More Margin

- `step=2250 length=6750 keep=0 ac=1280`
  - median `WER 12.2%`
  - median `enc 1112.5ms`
  - median headroom `1137.5ms`
- `step=2500 length=5000 keep=0 ac=1280`
  - median `WER 12.8%`
  - median `enc 1106.0ms`
  - median headroom `1394.0ms`
- `step=3000 length=6000 keep=0 ac=1280`
  - median `WER 13.5%`
  - median `enc 1099.2ms`
  - median headroom `1900.8ms`

### Current Recommendation

- Accuracy-first profile: `2250/6750 keep=100 ac=1500`
- Balanced default candidate: `2500/5000 keep=0 ac=1280`
- Conservative busy-laptop profile: `3000/6000 keep=0 ac=1280`

These still need one more round under synthetic system load before they should be treated as final defaults.

## Diary

### 2026-03-14

- Started a fresh adversarial troubleshooting session after earlier benchmark work got stuck on virtual-audio issues.
- Confirmed the core wiring problem is solved: benchmark traffic can be replayed through a virtual PipeWire source into the same SDL2/`whisper-stream` path as production.
- Found that some poor early transcripts were not routing bugs but real scoring/output issues.
- Measured repeated-word overlap at chunk boundaries and fixed it in the live stream parser.
- Added tests for overlap handling and bench-side text merging.
- Downloaded `ggml-medium.bin` for upcoming CPU and quantized-medium experiments.
- Decided to keep a persistent experiment log so overnight runs and tomorrow-morning review have continuity.
- Fixed a benchmark footgun: explicit `keep=0` is now preserved instead of silently turning back into `200ms`.
- Added `dictate: whisper-stream args=...` logging so future runs show the exact effective parameters sent upstream.
- Audited upstream `ggml` CMake behavior: `GGML_NATIVE` defaults to `ON` on non-cross native builds.
- Parameterized the Docker build so we can compare `GGML_NATIVE=ON` vs `GGML_NATIVE=OFF` explicitly (`make whisper-native`, `make whisper-generic`). These comparison binaries now live under `.build/` so `bin/` remains the main app path.
- Native-vs-generic build audit result: Docker native build clearly enables `-march=native` (`Adding CPU backend variant ggml-cpu: -march=native` in CMake output).
- Early CPU re-validation for turbo q5:
  - native build, `ac=0`: forced kill after settle window; no final timings; `WER 73.7%`
  - generic build, `ac=0`: forced kill after settle window; no final timings; `WER 80.1%`
  - native build, `ac=768`: graceful exit, but still far too slow for realtime (`encode 5800ms` on a `3000ms` step, headroom `-2800ms`, `WER 51.3%`)
  - generic build, `ac=768`: still forced kill, `WER 60.3%`
- Provisional conclusion: this laptop CPU is not a viable realtime path for turbo q5; native build helps, but not nearly enough.
- Downloaded `ggml-medium-q5_0.bin` directly (preferred over local quantization for speed of experimentation).
- Early medium CPU results on native build:
  - `ggml-medium-q5_0.bin`, `ac=0`: `encode 4975ms`, headroom `-1975ms`, `WER 63.5%`
  - `ggml-medium-q5_0.bin`, `ac=768`: `encode 2483ms`, headroom `+516ms`, `WER 30.8%`
  - `ggml-medium.bin`, `ac=0`: forced kill after settle window, `WER 60.9%`
  - `ggml-medium.bin`, `ac=768`: `encode 2956ms`, headroom `+44ms`, `WER 38.5%`
- Provisional conclusion: full medium on CPU is borderline at best even with `ac=768`; quantized medium q5 with `ac=768` is the first CPU configuration that looks plausibly testable further on this machine.
- Added `bench/run_overnight_gpu_sweep.py` to automate an overnight two-stage GPU search:
  - stage 1: coarse `ac` sweep at the baseline config
  - stage 2: grid over `step`, `length`, and `keep` using the best `ac` values from stage 1
  - outputs per-run raw logs, a TSV, summaries, finalists, and a diary under `bench/results/overnight-*`
- Launched an overnight GPU session: `bench/results/overnight-20260314-gpu-sweep/` with launcher log `bench/results/overnight-20260314-gpu-sweep.launch.log`.
- Output fanout now carries both raw and annotated text, so typed sinks can use clean text while stdout/files can keep timestamps.
- Added `scripts/toggle-dictate.sh` so one keyboard shortcut can start dictation into the focused window and the same shortcut can stop it reliably. Manual stop is now the primary control path; silence timeout is secondary only.
- Added a Go integration test (`integ/bench_test.go`) and `make integ-test` as the single default-profile regression check against corpus WER, headroom, and clean shutdown.

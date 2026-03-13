# dictate

Voice-to-text for Linux terminals. Speak instead of type — into opencode, claude-code, neovim, or any focused window.

## Goal

A lightweight dictation tool that captures microphone audio, streams it through whisper.cpp for local speech recognition, and routes the transcribed text to a configurable output: stdout, file, clipboard, or direct keystroke injection.

Primary use cases:
- Dictate to a terminal running an AI coding assistant (opencode, claude-code, gpt-codex)
- Dictate into neovim in insert mode
- Dictate to clipboard for pasting anywhere

Constraints:
- Local inference only (whisper.cpp, no cloud APIs)
- Low latency, streaming transcription
- Works on Wayland (sway, GNOME, KDE)
- Must work on both powerful workstations (Ryzen 9, 24C) and old laptops (ThinkPad T460, 2C/4T)

## Architecture

```
┌─────────────────────────────────────────┐
│        whisper.cpp `whisper-stream`      │
│  SDL2 captures mic → sliding window     │
│  inference → text on stdout             │
└──────────────────┬──────────────────────┘
                   │ pipe (stdout)
┌──────────────────▼──────────────────────┐
│           dictate (Go binary)           │
│  - parses stream stdout (strips ANSI)   │
│  - filters hallucinations               │
│  - timestamps each output line          │
│  - SIGUSR1 toggles start/stop           │
│  - routes text to output sink(s)        │
└──────────────────┬──────────────────────┘
                   │
          ┌────────┼────────┐
          ▼        ▼        ▼
       stdout   wl-copy   wtype
     (default) (clipboard) (keys)
       + optional --file tee
```

Two processes connected by a pipe. `whisper-stream` handles all audio capture (via SDL2, which uses PipeWire as its backend on modern Linux) and inference. The Go binary manages lifecycle, parses output, and routes text.

### Key design decisions

- **whisper-stream does the hard work**: audio capture, sliding-window inference, streaming output. No reason to reimplement any of it.
- **Go for orchestration**: subprocess management, signal handling, output routing. Fast iteration, no CGo needed.
- **SDL2 for audio**: whisper-stream uses SDL2, which auto-detects PipeWire/PulseAudio/ALSA at runtime. No audio code on our side.
- **PipeWire-native mic detection**: `pw-dump` to enumerate sources, heuristic scoring (USB > BT > DMIC > Analog), per-process routing via `PIPEWIRE_NODE` env var (no system-wide side effects).
- **Hallucination filtering**: whisper hallucinates on silence (CJK text, "thank you for watching", etc.). Go parser drops non-Latin script and known phantom phrases.

### Build closure

Docker multi-stage build. All build-time dependencies (gcc, cmake, libsdl2-dev, libvulkan-dev, glslc, Go) live in the container. Host needs only Docker to build.

Output: two binaries (`whisper-stream` ~45MB with Vulkan shaders, `dictate` ~2.5MB static Go).

### Runtime dependencies

`whisper-stream` dynamically links against SDL2 and Vulkan. The Go binary is statically linked.

| Dep | Debian/Ubuntu | Fedora | Arch |
|---|---|---|---|
| SDL2 (audio capture) | `libsdl2-2.0-0` | `SDL2` | `sdl2` |
| Vulkan (GPU, optional) | `libvulkan1` | `vulkan-loader` | `vulkan-icd-loader` |

Debian/Ubuntu: `sudo ./scripts/install-runtime.sh`
Fedora: `sudo dnf install SDL2 vulkan-loader`
Arch: `sudo pacman -S sdl2 vulkan-icd-loader`

### Output parsing

`whisper-stream` uses ANSI escape codes (`\033[2K\r`) to overwrite lines as transcription refines. Every N steps it emits `\n` to finalize.

The Go parser:
1. Reads stdout line-by-line (split on `\n`)
2. Splits each line by `\r`, takes the last segment (final refinement)
3. Strips ANSI escape codes
4. Drops whisper special tokens (`[BLANK_AUDIO]`, `[Start speaking]`, etc.)
5. Drops hallucination text (non-Latin scripts, known phrases)
6. Prepends timestamp with cycle delta: `[MM:SS.s Δ3.2s] transcribed text`

### Streaming parameters

- `--step 3000` (3s): inference runs every 3 seconds
- `--length 8000` (8s): each inference window is 8 seconds of audio
- `--keep 200`: 200ms of audio context kept between windows
- Encode time: ~2200ms/step on Radeon 890M Vulkan with large-v3-turbo-q5_0
- With 3s step, ~800ms headroom per cycle (no audio drops)

Effective latency: **~3-4 seconds** from speech to text on stdout.

### Toggle mechanism

Unix signals. Sway keybinding sends `pkill -USR1 dictate`:
- SIGUSR1: toggle recording on/off (kills/restarts whisper-stream)
- SIGTERM/SIGINT: clean shutdown

## CLI

```
dictate [--model path] [--lang auto|en|pt] [--device ID|name] [--file path]
        [--cpu] [--list-devices] [--pw-node ID]
        [--step ms] [--length ms] [--keep ms] [--ac N]
```

- `--model`: path to ggml model. Default: largest `ggml-*.bin` in `models/`
- `--lang`: language for transcription. Default: `auto`
- `--device`: PipeWire node ID or name substring. Default: auto-detect best mic
- `--pw-node`: PipeWire node ID, bypasses mic detection (for benchmarks / direct control)
- `--file`: also tee output to a file (append mode)
- `--cpu`: disable GPU (Vulkan) inference, use CPU only
- `--list-devices`: print audio sources and exit
- `--step`: inference interval in ms. Default: `3000`
- `--length`: audio window length in ms. Default: `8000`
- `--keep`: audio context kept between windows in ms. Default: `200`
- `--ac`: audio context limit (0 = whisper default). Default: `0`

Output goes to **stdout** by default. All log/diagnostic output goes to stderr.

## Models

Multilingual models (not `.en` variants) — supports Portuguese, English, and auto-detect.

| Model | Size | Vulkan (890M) | CPU (24T) | Quality | Notes |
|---|---|---|---|---|---|
| `ggml-tiny.bin` | 75MB | fast | fast | poor | Low-power machines |
| `ggml-base.bin` | 142MB | fast | fast | fair | Fast iteration/testing |
| `ggml-small.bin` | 466MB | fast | ~real-time | good | General use |
| `ggml-large-v3-turbo-q5_0.bin` | 548MB | Δ3.0s/step | too slow | good | Best tested so far |

Default: auto-selects the largest model present in `models/`.

### Findings

- **Turbo q5 on Vulkan is the sweet spot**: Δ3.0s/step on Radeon 890M, good Portuguese accuracy with `--lang pt`.
- **CPU cannot keep up with turbo**: 12-15s per 3s step even with all 24 threads. CPU is viable only with smaller models (base, small).
- **Language auto-detect is unreliable for Portuguese**: 25-30% misdetection (Spanish, French, German, English). Use `--lang pt` explicitly.
- **Performance power profile recommended**: laptop mode works but performance profile gives the iGPU more thermal headroom.
- **GPU at 100% is expected**: Vulkan shaders (clip rectangle, shader interpolator) saturate the iGPU during encode. This is fine — the 890M is shared with display compositor but doesn't cause visible issues in performance profile.

## Task Breakdown

### Phase 1 — MVP: stream to stdout ✅ DONE

- [x] Project scaffold (dirs, go.mod, .gitignore, LICENSE)
- [x] DESIGN.md
- [x] Dockerfile (multi-stage: whisper.cpp with Vulkan + Go)
- [x] Makefile (all, build, models, lint, run, clean)
- [x] scripts/install-runtime.sh
- [x] Go code: main.go, process.go, file.go, detect.go
- [x] Docker build produces both binaries (CPU + Vulkan)
- [x] Download multilingual models (tiny, base, small, large-v3-turbo-q5_0)
- [x] PipeWire mic auto-detection with heuristic scoring
- [x] Per-process audio routing via PIPEWIRE_NODE (no system-wide mutation)
- [x] Hallucination filtering (non-Latin, known phrases)
- [x] Timestamp prefixes on output for latency visibility
- [x] Tuned streaming params (3s step, 8s window, 200ms keep)
- [x] Tested on hardware — transcription works (en + pt)
- [x] GitHub repo + license

### Phase 2 — Quality + latency ← CURRENT

- [x] Test large-v3-turbo-q5_0 model — good quality, Δ3.0s on Vulkan
- [x] Test Vulkan GPU vs CPU — Vulkan required for turbo, CPU too slow (12-15s/step)
- [x] Tune step/length — 3000/8000 works, needs deeper understanding
- [x] Per-step delta timing (Δ) in output for latency visibility
- [x] `--cpu` flag to disable Vulkan when needed
- [x] Better Portuguese accuracy — `--lang pt` explicit, auto-detect too unreliable
- [ ] Understand step/length/keep interaction, find optimal values
- [ ] Test `-ac 768` (audio context limit) — used by whisper.cpp author for better perf
- [ ] Explore alternative/newer whisper models
- [ ] Try quantized medium model (medium-q5 ~250MB, possibly faster than turbo-q5)
- [ ] Evaluate whisper-stream VAD mode vs step mode

### Phase 2.5 — Benchmark harness

Automated search for optimal model + settings. Exercises the exact production
code path (whisper-stream + dictate via virtual PipeWire source), not whisper-cli.

- [x] Parameterize streaming params (step/length/keep/ac) as CLI flags
- [x] `--pw-node` flag to bypass mic detection (direct PipeWire node ID)
- [x] Virtual PipeWire source: `pactl load-module module-null-sink` creates virtual
      sink, `pw-cat --playback` injects WAV, whisper-stream captures from monitor
- [x] Benchmark runner (`cmd/bench`): sweep step × length × keep × ac combos
- [x] WER scoring against reference transcripts
- [x] WAV test corpus: JFK sample from whisper.cpp (`make corpus`)
- [ ] Test the pipeline end-to-end (virtual source → whisper-stream → dictate → WER)
- [ ] Add Portuguese corpus (record or download from Common Voice)
- [ ] Equivalence validation: compare virtual replay vs live mic on same audio
- [ ] Rank combos by accuracy × speed, find Pareto frontier

### Phase 3 — Clipboard + toggle UX

- [ ] SIGUSR1 toggle tested and documented
- [ ] `--output clipboard` mode (pipes text to `wl-copy`)
- [ ] Sway keybinding config example
- [ ] Desktop notification on toggle (via `notify-send`)

### Phase 4 — Keystroke injection

- [ ] `--output type` mode using `wtype` (Wayland keystroke injection)
- [ ] Works with terminals (types directly into focused window)
- [ ] Works with neovim in insert mode

### Phase 5 — Polish + portability

- [ ] Config file (~/.config/dictate/config.toml)
- [ ] Visual indicator (sway bar or desktop notification)
- [ ] Packaging: portable tarball (binaries + models)
- [ ] Test on ThinkPad T460 with tiny model
- [ ] Nix flake (future, for hermetic deployment)

## File Layout

```
dictate/
├── README.md
├── AGENTS.md
├── LICENSE
├── Dockerfile
├── Makefile
├── go.mod
├── .gitignore
├── scripts/
│   └── install-runtime.sh
├── cmd/
│   ├── dictate/
│   │   └── main.go
│   └── bench/
│       └── main.go          # benchmark runner (WER scoring, param sweep)
├── internal/
│   ├── audio/
│   │   └── detect.go        # PipeWire mic detection
│   ├── whisper/
│   │   └── process.go       # whisper-stream subprocess + parser
│   └── output/
│       └── file.go           # Sink interface: stdout, file, multi
├── bench/
│   └── corpus/               # test WAV + reference transcripts (WAVs gitignored)
├── bin/                       # build output (gitignored)
└── models/                    # downloaded models (gitignored)
```

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

Target: Debian/Ubuntu. `whisper-stream` dynamically links against:
- `libsdl2-2.0-0` (audio capture → PipeWire backend)
- `libvulkan1` (GPU inference, optional — CPU fallback works)

Installed once via `scripts/install-runtime.sh`. The Go binary is statically linked.

### Output parsing

`whisper-stream` uses ANSI escape codes (`\033[2K\r`) to overwrite lines as transcription refines. Every N steps it emits `\n` to finalize.

The Go parser:
1. Reads stdout line-by-line (split on `\n`)
2. Splits each line by `\r`, takes the last segment (final refinement)
3. Strips ANSI escape codes
4. Drops whisper special tokens (`[BLANK_AUDIO]`, `[Start speaking]`, etc.)
5. Drops hallucination text (non-Latin scripts, known phrases)
6. Prepends timestamp: `[MM:SS.s] transcribed text`

### Streaming parameters

- `--step 2000` (2s): inference runs every 2 seconds
- `--length 5000` (5s): each inference window is 5 seconds of audio
- `n_new_line = 1`: text finalized every step (~2.8s with inference)
- Encode time: ~800ms/step on Ryzen 9 24C with base model

Effective latency: **~2-3 seconds** from speech to text on stdout.

### Toggle mechanism

Unix signals. Sway keybinding sends `pkill -USR1 dictate`:
- SIGUSR1: toggle recording on/off (kills/restarts whisper-stream)
- SIGTERM/SIGINT: clean shutdown

## CLI

```
dictate [--model path] [--lang auto|en|pt] [--device ID|name] [--file path]
        [--list-devices]
```

- `--model`: path to ggml model. Default: largest `ggml-*.bin` in `models/`
- `--lang`: language for transcription. Default: `auto`
- `--device`: PipeWire node ID or name substring. Default: auto-detect best mic
- `--file`: also tee output to a file (append mode)
- `--list-devices`: print audio sources and exit

Output goes to **stdout** by default. All log/diagnostic output goes to stderr.

## Models

Multilingual models (not `.en` variants) — supports Portuguese, English, and auto-detect.

| Model | Size | Speed (Ryzen 9) | Quality | Use case |
|---|---|---|---|---|
| `ggml-tiny.bin` | 75MB | real-time++ | poor | Low-power machines (T460) |
| `ggml-base.bin` | 142MB | real-time++ | fair | Fast iteration/testing |
| `ggml-small.bin` | 466MB | real-time+ | good | General use |
| `ggml-large-v3-turbo-q5_0.bin` | 548MB | TBD | best? | Accuracy-first, needs testing |

Default: auto-selects the largest model present in `models/`.

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
- [x] Tuned streaming params (2s step, 5s window)
- [x] Tested on hardware — transcription works (en + pt)
- [x] GitHub repo + license

### Phase 2 — Quality + latency ← CURRENT

- [ ] Test large-v3-turbo-q5_0 model (accuracy vs speed tradeoff)
- [ ] Test Vulkan GPU inference (Radeon 890M) vs CPU
- [ ] Tune step/length for optimal latency/accuracy tradeoff
- [ ] Measure and log actual audio-to-text latency
- [ ] Evaluate whisper-stream VAD mode vs step mode
- [ ] Better Portuguese accuracy (explicit --lang pt vs auto)

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
├── DESIGN.md
├── LICENSE
├── Dockerfile
├── Makefile
├── go.mod
├── .gitignore
├── scripts/
│   └── install-runtime.sh
├── cmd/
│   └── dictate/
│       └── main.go
├── internal/
│   ├── audio/
│   │   └── detect.go       # PipeWire mic detection
│   ├── whisper/
│   │   └── process.go      # whisper-stream subprocess + parser
│   └── output/
│       └── file.go          # Sink interface: stdout, file, multi
├── bin/                      # build output (gitignored)
└── models/                   # downloaded models (gitignored)
```

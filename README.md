# dictate

Voice-to-text for Linux terminals. Speak instead of type — into opencode, claude-code, neovim, or any focused window.

## Goal

A lightweight dictation tool that captures microphone audio, streams it through whisper.cpp for local speech recognition, and routes the transcribed text to a configurable output: stdout, files, or direct keystroke injection.

Primary use cases:
- Dictate to a terminal running an AI coding assistant (opencode, claude-code, gpt-codex)
- Dictate into neovim in insert mode
- Dictate directly into the focused terminal or text box on Wayland

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
│  - keeps raw + annotated text streams   │
│  - SIGUSR1 toggles start/stop           │
│  - routes text to output sink(s)        │
└──────────────────┬──────────────────────┘
                   │
          ┌────────┼─────────┐
          ▼        ▼         ▼
       stdout   raw-file   wtype
    (annotated)  (raw)    (keys)
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

Normal app builds live in `bin/`. CPU-comparison variants from `make whisper-native` and
`make whisper-generic` live in `.build/` so the main app path stays unambiguous.

### Runtime dependencies

`whisper-stream` dynamically links against SDL2 and Vulkan. The Go binary is statically linked.

| Dep | Debian/Ubuntu | Fedora | Arch |
|---|---|---|---|
| SDL2 (audio capture) | `libsdl2-2.0-0` | `SDL2` | `sdl2` |
| Vulkan (GPU, optional) | `libvulkan1` | `vulkan-loader` | `vulkan-icd-loader` |
| wtype (typed output) | `wtype` | `wtype` | `wtype` |

Debian/Ubuntu: `sudo ./scripts/install-runtime.sh`
Fedora: `sudo dnf install SDL2 vulkan-loader`
Arch: `sudo pacman -S sdl2 vulkan-icd-loader wtype`

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

- Balanced default profile: `--step 2500 --length 5000 --keep 0 --ac 1280`
- `--step 2500` (2.5s): inference runs every 2.5 seconds
- `--length 5000` (5s): each inference window targets 5 seconds of audio
- `--keep 0`: rely on text-side overlap trimming instead of forced audio overlap
- `--ac 1280`: slightly reduced audio context, faster than full-context `1500` with little quality loss
- Median encode time on Radeon 890M Vulkan: ~1106ms/step
- Median headroom per cycle: ~1394ms (no audio drops in overnight sweep)

Effective latency: **~2.5-3 seconds** from speech to text on stdout.

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
- `--output`: `stdout` or `type`. Default: `stdout`
- `--pw-node`: PipeWire node ID, bypasses mic detection (for benchmarks / direct control)
- `--file`: also tee output to a file (append mode)
- `--raw-file`: also tee raw text chunks to a file (append mode, no timestamps)
- `--cpu`: disable GPU (Vulkan) inference, use CPU only
- `--list-devices`: print audio sources and exit
- `--step`: inference interval in ms. Default: `2500`
- `--length`: audio window length in ms. Default: `5000`
- `--keep`: audio context kept between windows in ms. Default: `0`
- `--ac`: audio context limit (0 = whisper default). Default: `1280`
- `--silence-timeout`: stop after this much transcription silence. Example: `15s`

Output goes to **stdout** by default. All log/diagnostic output goes to stderr.

Typing into the focused window on Wayland:

```bash
dictate --output type --silence-timeout 15s
```

One-key toggle for a focused input box:

```bash
scripts/toggle-dictate.sh start --lang en
scripts/toggle-dictate.sh stop
scripts/toggle-dictate.sh status
```

All development and testing was done on sway (Regolith/Wayland). The core
pipeline (whisper-stream → dictate → wtype) works on any Wayland compositor.
Only the keybinding setup differs per desktop environment.

### Sway / i3-sway

```bash
bindsym $mod+d exec --no-startup-id "$HOME/play/dictate/scripts/toggle-dictate.sh" toggle
```

For laptop media keys (e.g. the display-toggle button on F9), the firmware
often translates the keypress into a modifier combo rather than an XF86
keysym. On Framework laptops with fn-row defaulting to media functions,
F9 (display icon) sends `Super+P`:

```bash
bindsym $mod+p exec --no-startup-id "$HOME/play/dictate/scripts/toggle-dictate.sh" toggle
```

If your firmware emits a raw XF86 keysym instead, use that directly:

```bash
bindsym XF86Display exec --no-startup-id "$HOME/play/dictate/scripts/toggle-dictate.sh" toggle
```

To check what your key actually sends, use `wev -f wl_keyboard` and look
at the `sym:` line for the pressed event.

### Hyprland

```
bind = SUPER, P, exec, ~/play/dictate/scripts/toggle-dictate.sh toggle
```

### GNOME (Wayland)

Settings → Keyboard → Custom Shortcuts, or via CLI:

```bash
# create the shortcut
dconf write /org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/dictate/name "'Dictate Toggle'"
dconf write /org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/dictate/command "'/path/to/dictate/scripts/toggle-dictate.sh toggle'"
dconf write /org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/dictate/binding "'<Super>p'"

# register it
dconf write /org/gnome/settings-daemon/plugins/media-keys/custom-keybindings "['/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/dictate/']"
```

### KDE Plasma (Wayland)

System Settings → Shortcuts → Custom Shortcuts → Add new → Command/URL.
Set the trigger key and point the action at `toggle-dictate.sh toggle`.

### X11

`wtype` only works on Wayland. X11 would need `xdotool type` as the
keystroke backend, which is not yet implemented.

The toggle script writes state under `${XDG_RUNTIME_DIR}/dictate/` (`dictate.pid`, `dictate.log`).
Use the shortcut again for a hard stop. This is more reliable than silence timeout when nearby voices keep the model active.

Recommended model targets:

```bash
make model-gpu        # ggml-large-v3-turbo-q5_0.bin
make model-cpu-light  # ggml-medium-q5_0.bin
make models-recommended
```

Single-command regression check for the default profile:

```bash
make integ-test
```

This runs a Go integration test that exercises the production-path benchmark with the
current default settings and fails if:
- median WER across repeated runs exceeds the configured threshold (default `22%`)
- any run has to be force-killed instead of exiting cleanly
- median headroom drops below the configured floor (default `500ms`)

Override knobs if needed:

```bash
DICTATE_INTEG_REPEATS=3 DICTATE_INTEG_MAX_MEDIAN_WER=22 DICTATE_INTEG_MIN_MEDIAN_HEADROOM_MS=500 make integ-test
```

## Models

Multilingual models (not `.en` variants) — supports Portuguese, English, and auto-detect.

| Model | Size | Vulkan (890M) | CPU (24T) | Quality | Notes |
|---|---|---|---|---|---|
| `ggml-tiny.bin` | 75MB | fast | fast | poor | Low-power machines |
| `ggml-base.bin` | 142MB | fast | fast | fair | Fast iteration/testing |
| `ggml-small.bin` | 466MB | fast | ~real-time | good | General use |
| `ggml-large-v3-turbo-q5_0.bin` | 548MB | Δ2.5s/step | too slow | best | Best current default on GPU |

Default: auto-selects the largest model present in `models/`.

### Findings

- **Turbo q5 on Vulkan is the sweet spot**: balanced default is `2500/5000 keep=0 ac=1280`; accuracy-first can push to `2250/6750 keep=100 ac=1500`.
- **CPU cannot keep up with turbo**: even with a native-optimized build, turbo q5 CPU stayed far from realtime on this 24-thread laptop.
- **Medium q5 on CPU is only borderline**: `ggml-medium-q5_0.bin` with `ac=768` is the first CPU config that looked remotely plausible, but GPU still wins comfortably.
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
- [x] Tuned streaming params (balanced default: 2.5s step, 5s window, 0ms keep, `ac=1280`)
- [x] Tested on hardware — transcription works (en + pt)
- [x] GitHub repo + license

### Phase 2 — Quality + latency ← CURRENT

- [x] Test large-v3-turbo-q5_0 model — good quality, Δ3.0s on Vulkan
- [x] Test Vulkan GPU vs CPU — Vulkan required for turbo, CPU too slow (12-15s/step)
- [x] Tune step/length/keep/ac — overnight sweep picked balanced, accuracy-first, and conservative profiles
- [x] Per-step delta timing (Δ) in output for latency visibility
- [x] `--cpu` flag to disable Vulkan when needed
- [x] Better Portuguese accuracy — `--lang pt` explicit, auto-detect too unreliable
- [x] Understand step/length/keep interaction well enough to choose balanced and conservative defaults
- [x] Test `-ac` sweep and rank candidate context caps
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
- [x] Rank combos by accuracy × speed, find Pareto frontier

### Phase 3 — Toggle UX

- [x] SIGUSR1 toggle documented
- [x] Sway keybinding config example
- [ ] Desktop notification on toggle (via `notify-send`)

### Phase 4 — Keystroke injection

- [x] `--output type` mode using `wtype` (Wayland keystroke injection)
- [x] Works with terminals (types directly into focused window)
- [ ] Works with neovim in insert mode

### Phase 5 — Polish + portability

- [ ] Config file (~/.config/dictate/config.toml)
- [ ] Visual indicator (sway bar or desktop notification)
- [ ] Packaging: portable tarball (binaries + models)
- [ ] Test on ThinkPad T460 with tiny model
- [ ] Nix flake (future, for hermetic deployment)

### Phase 6 — Beyond whisper: better ASR models

whisper.cpp only supports whisper-family models. The ASR landscape has moved past whisper — these are worth exploring as a replacement for `whisper-stream`:

- **NVIDIA Parakeet TDT v3** (0.6B, CC-BY-4.0): 6.3% WER, built-in punctuation & capitalization, 25 EU languages (EN + PT), runs via ONNX on CPU at RTFx ~3000+. Community consensus as the current sweet spot. Has chunked streaming support.
- **NVIDIA Nemotron Speech Streaming** (0.6B, NVIDIA Open): 6.9% WER, cache-aware native streaming down to 80ms chunks. EN-only. Purpose-built for real-time dictation. Released March 2026.
- **IBM Granite 4.0 1B Speech** (2B total, Apache 2.0): 5.5% WER, lowest on Open ASR leaderboard. Batch only (no streaming). Has keyword biasing for names/acronyms. EN, PT, FR, DE, ES, JA.

Integration path: replace `whisper-stream` with an ONNX or NeMo-based streaming subprocess. The Go binary and pipe architecture stay the same. Open whisper.cpp issues (#1732, #3118) requesting Parakeet support have no implementation.

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
├── audio/
│   └── detect.go             # PipeWire mic detection
├── whisper/
│   └── process.go            # whisper-stream subprocess + parser
├── output/
│   └── file.go               # sink fanout: stdout, file, wtype
├── integ/
│   └── bench_test.go         # production-path integration test
├── bench/
│   └── corpus/               # test WAV + reference transcripts (WAVs gitignored)
├── bin/                       # main app binaries
├── .build/                    # benchmark-only binary variants
└── models/                    # downloaded models (gitignored)
```

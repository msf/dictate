package whisper

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Config holds all parameters for a whisper-stream subprocess.
type Config struct {
	StreamBin string
	Model     string
	Lang      string
	Threads   int
	PwNodeID  int
	CPUOnly   bool
	OnText    func(string)

	// Streaming parameters (ms). Step/Length default only when unset.
	// Keep=0 is meaningful and must be preserved.
	Step   int // inference interval (default 3000 when <= 0)
	Length int // audio window (default 8000 when <= 0)
	Keep   int // context kept between windows (default 200 only when < 0)
	AC     int // audio context limit (0 = whisper default)
}

// Process manages a whisper-stream subprocess.
type Process struct {
	cfg       Config
	startTime time.Time
	lastEmit  time.Time
	lastText  string

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

func NewProcess(cfg Config) *Process {
	if cfg.Step == 0 {
		cfg.Step = 3000
	}
	if cfg.Length == 0 {
		cfg.Length = 8000
	}
	if cfg.Keep < 0 {
		cfg.Keep = 200
	}
	return &Process{cfg: cfg}
}

func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startLocked()
}

func (p *Process) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *Process) Toggle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		p.stopLocked()
		fmt.Fprintf(os.Stderr, "dictate: paused\n")
	} else {
		if err := p.startLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "dictate: resume failed: %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "dictate: resumed\n")
	}
}

func (p *Process) startLocked() error {
	args := streamArgs(p.cfg)
	fmt.Fprintf(os.Stderr, "dictate: whisper-stream args=%s\n", strings.Join(args, " "))
	p.cmd = exec.Command(p.cfg.StreamBin, args...)

	// Route SDL2 audio capture to our chosen PipeWire node.
	// Per-process only — no system-wide side effects.
	if p.cfg.PwNodeID > 0 {
		p.cmd.Env = append(os.Environ(), fmt.Sprintf("PIPEWIRE_NODE=%d", p.cfg.PwNodeID))
	}

	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	p.cmd.Stderr = os.Stderr
	p.startTime = time.Now()
	p.lastEmit = p.startTime
	p.lastText = ""

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("start whisper-stream: %w", err)
	}

	p.running = true

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			text := parseLine(scanner.Text())
			if text == "" || isHallucination(text) {
				continue
			}
			text = trimLeadingOverlap(p.lastText, text)
			if text == "" {
				continue
			}
			p.lastText = text
			now := time.Now()
			elapsed := now.Sub(p.startTime).Truncate(100 * time.Millisecond)
			delta := now.Sub(p.lastEmit).Truncate(100 * time.Millisecond)
			p.lastEmit = now
			stamped := fmt.Sprintf("[%s Δ%.1fs] %s", formatDuration(elapsed), delta.Seconds(), text)
			p.cfg.OnText(stamped)
		}
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	return nil
}

func streamArgs(cfg Config) []string {
	args := []string{
		"-m", cfg.Model,
		"-l", cfg.Lang,
		"-t", fmt.Sprintf("%d", cfg.Threads),
		"--step", fmt.Sprintf("%d", cfg.Step),
		"--length", fmt.Sprintf("%d", cfg.Length),
		"--keep", fmt.Sprintf("%d", cfg.Keep),
	}
	if cfg.AC > 0 {
		args = append(args, "-ac", fmt.Sprintf("%d", cfg.AC))
	}
	if cfg.CPUOnly {
		args = append(args, "-ng")
	}
	return args
}

func (p *Process) stopLocked() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	_ = p.cmd.Wait()
	p.cmd = nil
	p.running = false
}

// parseLine extracts clean text from whisper-stream stdout.
func parseLine(raw string) string {
	parts := strings.Split(raw, "\r")
	last := parts[len(parts)-1]
	clean := ansiRe.ReplaceAllString(last, "")
	clean = strings.TrimSpace(clean)

	if clean == "" {
		return ""
	}
	if strings.HasPrefix(clean, "[") && strings.HasSuffix(clean, "]") {
		return ""
	}
	return clean
}

func trimLeadingOverlap(prev, curr string) string {
	if prev == "" || curr == "" {
		return curr
	}
	prevWords := strings.Fields(prev)
	currWords := strings.Fields(curr)
	if len(prevWords) == 0 || len(currWords) == 0 {
		return curr
	}

	max := len(prevWords)
	if len(currWords) < max {
		max = len(currWords)
	}

	overlap := 0
	for k := max; k >= 1; k-- {
		ok := true
		for i := 0; i < k; i++ {
			if normalizeToken(prevWords[len(prevWords)-k+i]) != normalizeToken(currWords[i]) {
				ok = false
				break
			}
		}
		if ok {
			overlap = k
			break
		}
	}

	if overlap == 0 {
		return curr
	}
	if overlap >= len(currWords) {
		return ""
	}
	return strings.Join(currWords[overlap:], " ")
}

func normalizeToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isHallucination detects whisper artifacts from silence/noise.
func isHallucination(text string) bool {
	// Non-Latin script when we expect Latin languages (en/pt/es/etc).
	// Silence makes whisper hallucinate CJK, Arabic, etc.
	latin, nonLatin := 0, 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		if unicode.Is(unicode.Latin, r) {
			latin++
		} else {
			nonLatin++
		}
	}
	if nonLatin > 0 && nonLatin >= latin {
		return true
	}

	// Known hallucination phrases whisper produces on silence.
	lower := strings.ToLower(text)
	for _, h := range hallPhrases {
		if strings.Contains(lower, h) {
			return true
		}
	}

	return false
}

var hallPhrases = []string{
	"thank you for watching",
	"thanks for watching",
	"thank you for listening",
	"please subscribe",
	"sous-titres",
	"sous titres",
	"amara.org",
	"copyright",
	"subtitles by",
	"obrigado por assistir",
}

func formatDuration(d time.Duration) string {
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	ms := int(d.Milliseconds()) % 1000 / 100
	return fmt.Sprintf("%02d:%02d.%d", m, s, ms)
}

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

// Process manages a whisper-stream subprocess.
type Process struct {
	streamBin string
	model     string
	lang      string
	threads   int
	pwNodeID  int
	onText    func(string)
	startTime time.Time

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

func NewProcess(streamBin, model, lang string, threads, pwNodeID int, onText func(string)) *Process {
	return &Process{
		streamBin: streamBin,
		model:     model,
		lang:      lang,
		threads:   threads,
		pwNodeID:  pwNodeID,
		onText:    onText,
	}
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
	p.cmd = exec.Command(p.streamBin,
		"-m", p.model,
		"-l", p.lang,
		"-t", fmt.Sprintf("%d", p.threads),
		"--step", "2000",
		"--length", "5000",
		"--keep", "200",
	)

	// Route SDL2 audio capture to our chosen PipeWire node.
	// Per-process only — no system-wide side effects.
	if p.pwNodeID > 0 {
		p.cmd.Env = append(os.Environ(), fmt.Sprintf("PIPEWIRE_NODE=%d", p.pwNodeID))
	}

	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	p.cmd.Stderr = os.Stderr
	p.startTime = time.Now()

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
			elapsed := time.Since(p.startTime).Truncate(100 * time.Millisecond)
			stamped := fmt.Sprintf("[%s] %s", formatDuration(elapsed), text)
			p.onText(stamped)
		}
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	return nil
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

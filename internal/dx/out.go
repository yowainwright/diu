package dx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

type Tone uint8

const (
	Plain Tone = iota
	Accent
	Success
	Warning
	Error
	Info
	Muted
)

type Out struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	mu     sync.Mutex
}

func NewOut(stdout, stderr io.Writer, stdin io.Reader) *Out {
	return &Out{
		stdout: writerOrDiscard(stdout),
		stderr: writerOrDiscard(stderr),
		stdin:  readerOrEmpty(stdin),
	}
}

func TerminalOut() *Out {
	return NewOut(os.Stdout, os.Stderr, os.Stdin)
}

func (o *Out) Print(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprint(o.stdout, text)
}

func (o *Out) Printf(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stdout, format, args...)
}

func (o *Out) Println(args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintln(o.stdout, args...)
}

func (o *Out) UI(text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprint(o.stderr, text)
}

func (o *Out) UIPrintf(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stderr, format, args...)
}

func (o *Out) UILine(args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintln(o.stderr, args...)
}

func (o *Out) Status(tone Tone, message string) {
	prefix := Paint(o.stderr, tone, statusPrefix(tone))
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = fmt.Fprintf(o.stderr, "%s %s\n", prefix, message)
}

func (o *Out) DataStyle(tone Tone, text string) string {
	return Paint(o.stdout, tone, text)
}

func (o *Out) UIStyle(tone Tone, text string) string {
	return Paint(o.stderr, tone, text)
}

func (o *Out) Stdout() io.Writer {
	return o.stdout
}

func (o *Out) Stderr() io.Writer {
	return o.stderr
}

func (o *Out) Stdin() io.Reader {
	return o.stdin
}

func (o *Out) CanPrompt() bool {
	outsideCI := os.Getenv("CI") == ""
	standardTerminal := os.Getenv("TERM") != "dumb"
	canPromptInEnv := outsideCI && standardTerminal
	if !canPromptInEnv {
		return false
	}
	return IsTerminal(o.stdin) && IsTerminal(o.stderr)
}

func (o *Out) CanAnimate() bool {
	return animationEnabled(o.stderr)
}

func Paint(w io.Writer, tone Tone, text string) string {
	plainTone := tone == Plain
	useColor := colorEnabled(w)
	returnPlain := plainTone || !useColor
	if returnPlain {
		return text
	}
	code := toneCode(tone)
	if code == "" {
		return text
	}
	styled := "\x1b[" + code + "m" + text + "\x1b[0m"
	return styled
}

func IsTerminal(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	nullInfo, err := os.Stat(os.DevNull)
	if err != nil {
		return true
	}
	isDevNull := os.SameFile(info, nullInfo)
	return !isDevNull
}

func colorEnabled(w io.Writer) bool {
	colorAllowed := os.Getenv("NO_COLOR") == ""
	standardTerminal := os.Getenv("TERM") != "dumb"
	useColorInEnv := colorAllowed && standardTerminal
	if !useColorInEnv {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DIU_COLOR"))) {
	case "always":
		return true
	case "never":
		return false
	}
	isCI := os.Getenv("CI") != ""
	canUseColor := !isCI && IsTerminal(w)
	return canUseColor
}

func animationEnabled(w io.Writer) bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DIU_ACTIVITY"))) {
	case "always":
		return true
	case "never":
		return false
	}
	isCI := os.Getenv("CI") != ""
	canAnimate := !isCI && IsTerminal(w)
	return canAnimate
}

func toneCode(tone Tone) string {
	switch tone {
	case Accent:
		return "1;36"
	case Success:
		return "32"
	case Warning:
		return "33"
	case Error:
		return "31"
	case Info:
		return "36"
	case Muted:
		return "2"
	default:
		return ""
	}
}

func statusPrefix(tone Tone) string {
	switch tone {
	case Success:
		return "[ok]"
	case Warning:
		return "[!]"
	case Error:
		return "[x]"
	case Info:
		return "[i]"
	default:
		return "[>]"
	}
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func readerOrEmpty(r io.Reader) io.Reader {
	if r == nil {
		return strings.NewReader("")
	}
	return r
}

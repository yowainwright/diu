package dx

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

var (
	ErrCancelled      = errors.New("cancelled")
	ErrNonInteractive = errors.New("interactive terminal required")
)

type Choice struct {
	Label string
	Value string
}

type Prompter struct {
	reader *bufio.Reader
	writer io.Writer
}

func NewPrompter(reader io.Reader, writer io.Writer) *Prompter {
	return &Prompter{
		reader: bufferedReader(readerOrEmpty(reader)),
		writer: writerOrDiscard(writer),
	}
}

func TerminalPrompter() (*Prompter, error) {
	out := TerminalOut()
	if !out.CanPrompt() {
		return nil, ErrNonInteractive
	}
	return NewPrompter(os.Stdin, os.Stderr), nil
}

func (p *Prompter) Input(message string) (string, error) {
	p.writePrompt(message, "")
	return p.readLine()
}

func (p *Prompter) InputDefault(message, fallback string) (string, error) {
	p.writePrompt(message, " ["+fallback+"]")
	value, err := p.readLine()
	if err != nil {
		return "", err
	}
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func (p *Prompter) Confirm(message string, fallback bool) (bool, error) {
	hint := " [y/N]"
	if fallback {
		hint = " [Y/n]"
	}
	p.writePrompt(message, hint)
	value, err := p.readLine()
	return parseConfirmation(value, err, fallback)
}

func (p *Prompter) Select(message string, choices []Choice) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("prompt has no choices")
	}
	p.writeChoices(message, choices)
	value, err := p.readLine()
	if err != nil {
		return "", err
	}
	return selectedValue(value, choices)
}

func (p *Prompter) Require(message, expected string) error {
	p.writePrompt(message, "")
	value, err := p.readLine()
	if err != nil {
		return err
	}
	if value != expected {
		return ErrCancelled
	}
	return nil
}

func (p *Prompter) writePrompt(message, hint string) {
	marker := Paint(p.writer, Accent, "?")
	_, _ = fmt.Fprintf(p.writer, "%s %s%s: ", marker, message, hint)
}

func (p *Prompter) writeChoices(message string, choices []Choice) {
	marker := Paint(p.writer, Accent, "?")
	_, _ = fmt.Fprintf(p.writer, "%s %s\n", marker, message)
	for index, choice := range choices {
		_, _ = fmt.Fprintf(p.writer, "  %d. %s\n", index+1, choice.Label)
	}
	_, _ = fmt.Fprint(p.writer, "> ")
}

func (p *Prompter) readLine() (string, error) {
	value, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func parseConfirmation(value string, err error, fallback bool) (bool, error) {
	if err != nil {
		return false, err
	}
	switch strings.ToLower(value) {
	case "":
		return fallback, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid confirmation: %s", value)
	}
}

func selectedValue(value string, choices []Choice) (string, error) {
	selection, err := strconv.Atoi(value)
	if err != nil || selection < 1 || selection > len(choices) {
		return "", fmt.Errorf("invalid selection: %s", value)
	}
	return choices[selection-1].Value, nil
}

func bufferedReader(reader io.Reader) *bufio.Reader {
	if buffered, ok := reader.(*bufio.Reader); ok {
		return buffered
	}
	return bufio.NewReader(reader)
}

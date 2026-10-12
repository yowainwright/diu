package dx

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrCancelled = errors.New("cancelled")

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

func (p *Prompter) Input(message string) (string, error) {
	p.writePrompt(message)
	return p.readLine()
}

func (p *Prompter) Require(message, expected string) error {
	value, err := p.Input(message)
	if err != nil {
		return err
	}
	if value != expected {
		return ErrCancelled
	}
	return nil
}

func (p *Prompter) writePrompt(message string) {
	marker := Paint(p.writer, Accent, "?")
	_, _ = fmt.Fprintf(p.writer, "%s %s: ", marker, message)
}

func (p *Prompter) readLine() (string, error) {
	value, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func bufferedReader(reader io.Reader) *bufio.Reader {
	if buffered, ok := reader.(*bufio.Reader); ok {
		return buffered
	}
	return bufio.NewReader(reader)
}

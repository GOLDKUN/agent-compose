package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPromptLineReaderErasesWideRuneByDisplayWidth(t *testing.T) {
	var output bytes.Buffer
	input := &promptLineReader{
		stdin:    strings.NewReader("中文\x7f\r"),
		output:   &output,
		stdinFD:  0,
		outputFD: -1,
		makeRaw: func() (func() error, error) {
			return func() error { return nil }, nil
		},
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Fatalf("close prompt input: %v", err)
		}
	})

	line, err := input.ReadLine("agent@sandbox:> ")
	if err != nil {
		t.Fatalf("read prompt line: %v", err)
	}
	if line != "中" {
		t.Fatalf("prompt line = %q, want %q", line, "中")
	}
	lastRedraw := bytes.LastIndex(output.Bytes(), []byte("\x1b[2K\r"))
	if lastRedraw < 0 || bytes.Contains(output.Bytes()[lastRedraw:], []byte("文")) {
		t.Fatalf("wide rune erase output = %q, want final redraw without deleted rune", output.String())
	}
}

func TestPromptLineReaderStartsLineBeforeEnteringRawMode(t *testing.T) {
	var output bytes.Buffer
	input := &promptLineReader{
		stdin:    strings.NewReader("消息\r"),
		output:   &output,
		stdinFD:  0,
		outputFD: -1,
		makeRaw: func() (func() error, error) {
			if got, want := output.String(), "上一段输出\n"; got != want {
				t.Fatalf("output before raw mode = %q, want %q", got, want)
			}
			return func() error { return nil }, nil
		},
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Fatalf("close prompt input: %v", err)
		}
	})

	if _, err := io.WriteString(&output, "上一段输出"); err != nil {
		t.Fatalf("write preceding output: %v", err)
	}
	if err := input.StartLine(); err != nil {
		t.Fatalf("start prompt line: %v", err)
	}
	line, err := input.ReadLine("agent@sandbox:> ")
	if err != nil {
		t.Fatalf("read prompt line: %v", err)
	}
	if line != "消息" {
		t.Fatalf("prompt line = %q, want %q", line, "消息")
	}
}

func TestPromptLineReaderReturnsRawModeError(t *testing.T) {
	wantErr := errors.New("make raw failed")
	input := &promptLineReader{
		stdin:    strings.NewReader("消息\r"),
		output:   io.Discard,
		stdinFD:  0,
		outputFD: -1,
		makeRaw: func() (func() error, error) {
			return nil, wantErr
		},
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Fatalf("close prompt input: %v", err)
		}
	})

	line, err := input.ReadLine("agent@sandbox:> ")
	if !errors.Is(err, wantErr) {
		t.Fatalf("read prompt line error = %v (%q), want %v", err, line, wantErr)
	}
}

func TestPromptLineReaderEmptyInterruptIsNotEOF(t *testing.T) {
	var output bytes.Buffer
	input := &promptLineReader{
		stdin:    strings.NewReader("\x03"),
		output:   &output,
		stdinFD:  0,
		outputFD: -1,
		makeRaw: func() (func() error, error) {
			return func() error { return nil }, nil
		},
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Fatalf("close prompt input: %v", err)
		}
	})

	line, err := input.ReadLine("agent@sandbox:> ")
	if !errors.Is(err, errPromptInterrupted) {
		t.Fatalf("read prompt line error = %v (%q), want errPromptInterrupted", err, line)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("empty Ctrl-C must not look like EOF: %v", err)
	}
}

func TestPromptLineReaderNonEmptyInterruptContinues(t *testing.T) {
	var output bytes.Buffer
	input := &promptLineReader{
		stdin:    strings.NewReader("草稿\x03消息\r"),
		output:   &output,
		stdinFD:  0,
		outputFD: -1,
		makeRaw: func() (func() error, error) {
			return func() error { return nil }, nil
		},
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Fatalf("close prompt input: %v", err)
		}
	})

	line, err := input.ReadLine("agent@sandbox:> ")
	if err != nil {
		t.Fatalf("read prompt line: %v", err)
	}
	if line != "消息" {
		t.Fatalf("prompt line = %q, want %q", line, "消息")
	}
}

func TestPromptLineReaderTerminalInputWithoutTerminalOutputUsesScanner(t *testing.T) {
	var output bytes.Buffer
	reader := strings.NewReader("hello\n")
	input := &promptLineReader{
		stdin:    reader,
		output:   &output,
		scanner:  bufio.NewScanner(reader),
		stdinFD:  0,
		outputFD: -1,
	}
	if !input.IsTerminal() {
		t.Fatal("terminal stdin must retain terminal input semantics")
	}

	line, err := input.ReadLine("prompt: ")
	if err != nil {
		t.Fatalf("read prompt line: %v", err)
	}
	if line != "hello" {
		t.Fatalf("prompt line = %q, want %q", line, "hello")
	}
	if got, want := output.String(), "prompt: "; got != want {
		t.Fatalf("prompt output = %q, want plain output %q", got, want)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("redirected prompt output contains terminal control sequences: %q", output.String())
	}
}

func TestPromptLineReaderNonTerminalUsesScanner(t *testing.T) {
	var output bytes.Buffer
	input := newPromptLineReader(strings.NewReader("hello\nworld\n"), &output)
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Fatalf("close prompt input: %v", err)
		}
	})
	if input.IsTerminal() {
		t.Fatal("piped stdin must not be treated as a terminal")
	}

	line, err := input.ReadLine("prompt: ")
	if err != nil {
		t.Fatalf("read first line: %v", err)
	}
	if line != "hello" {
		t.Fatalf("first line = %q, want %q", line, "hello")
	}
	if got, want := output.String(), "prompt: "; got != want {
		t.Fatalf("prompt output = %q, want %q", got, want)
	}

	line, err = input.ReadLine("")
	if err != nil {
		t.Fatalf("read second line: %v", err)
	}
	if line != "world" {
		t.Fatalf("second line = %q, want %q", line, "world")
	}

	_, err = input.ReadLine("")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read after EOF = %v, want io.EOF", err)
	}
}

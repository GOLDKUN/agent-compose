package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"

	"github.com/chzyer/readline"
)

// errPromptInterrupted 表示用户在空输入行按下 Ctrl-C。
// 调用方不能将它视为 io.EOF，以免错误发送 stdin EOF。
var errPromptInterrupted = readline.ErrInterrupt

type promptLineReader struct {
	stdin    io.Reader
	output   io.Writer
	scanner  *bufio.Scanner
	terminal *readline.Instance
	stdinFD  int
	outputFD int
	makeRaw  func() (func() error, error)
}

func newPromptLineReader(stdin io.Reader, output io.Writer) *promptLineReader {
	r := &promptLineReader{stdin: stdin, output: output, stdinFD: -1, outputFD: -1}
	if fd, ok := terminalFileDescriptor(stdin); ok && isTerminalFD(fd) {
		r.stdinFD = fd
		if outputFD, outputOK := terminalFileDescriptor(output); outputOK && isTerminalFD(outputFD) {
			r.outputFD = outputFD
			// readline 依赖 OPOST 将提交输入时的 LF 归位到行首，不能使用会关闭 OPOST 的通用 raw 模式。
			r.makeRaw = func() (func() error, error) {
				state, err := readline.MakeRaw(fd)
				if err != nil {
					return nil, err
				}
				return func() error { return readline.Restore(fd, state) }, nil
			}
			return r
		}
	}
	r.scanner = bufio.NewScanner(stdin)
	r.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return r
}

func (r *promptLineReader) IsTerminal() bool {
	return r != nil && r.stdinFD >= 0
}

func (r *promptLineReader) StartLine() error {
	_, err := io.WriteString(r.output, "\n")
	return err
}

func (r *promptLineReader) ReadLine(prompt string) (string, error) {
	if r.scanner != nil {
		if prompt != "" {
			if _, err := io.WriteString(r.output, prompt); err != nil {
				return "", err
			}
		}
		if r.scanner.Scan() {
			return r.scanner.Text(), nil
		}
		if err := r.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}

	for {
		if r.terminal == nil {
			terminal, err := r.newTerminal(prompt)
			if err != nil {
				return "", err
			}
			r.terminal = terminal
		} else {
			r.terminal.SetPrompt(prompt)
		}
		// readline 会忽略 FuncMakeRaw 的错误，因此在读取前显式进入并检查结果。
		if err := r.terminal.Terminal.EnterRawMode(); err != nil {
			return "", fmt.Errorf("enter terminal raw mode: %w", err)
		}
		line, err := r.terminal.Readline()
		if errors.Is(err, readline.ErrInterrupt) {
			// 与 readline.Result.CanContinue 保持一致：非空输入被中断时清空并重新读取。
			if line != "" {
				continue
			}
			return "", errPromptInterrupted
		}
		return line, err
	}
}

func (r *promptLineReader) Close() error {
	if r == nil || r.terminal == nil {
		return nil
	}
	return r.terminal.Close()
}

func (r *promptLineReader) newTerminal(prompt string) (*readline.Instance, error) {
	var restore func() error
	return readline.NewEx(&readline.Config{
		Prompt:                 prompt,
		HistoryLimit:           -1,
		DisableAutoSaveHistory: true,
		Stdin:                  noCloseReader{Reader: r.stdin},
		Stdout:                 r.output,
		Stderr:                 r.output,
		ForceUseInteractive:    true,
		FuncIsTerminal:         func() bool { return true },
		FuncGetWidth: func() int {
			if size := terminalSizeForFD(r.outputFD); size != nil {
				return int(size.GetCols())
			}
			return 80
		},
		FuncMakeRaw: func() error {
			if restore != nil {
				return nil
			}
			var err error
			restore, err = r.makeRaw()
			return err
		},
		FuncExitRaw: func() error {
			if restore == nil {
				return nil
			}
			err := restore()
			restore = nil
			return err
		},
	})
}

type noCloseReader struct {
	io.Reader
}

func (noCloseReader) Close() error {
	return nil
}

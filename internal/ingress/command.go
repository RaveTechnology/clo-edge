package ingress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

type Runner interface {
	Run(context.Context, string, []string, []byte) ([]byte, error)
}
type ExecRunner struct{}
type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		return 0, errors.New("command output bound exceeded")
	}
	return b.buffer.Write(p)
}
func (ExecRunner) Run(ctx context.Context, program string, args []string, input []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = bytes.NewReader(input)
	out := boundedOutput{limit: 16 << 20}
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s command failed; no credential-bearing output logged", filepath.Base(program))
	}
	return out.Bytes(), nil
}

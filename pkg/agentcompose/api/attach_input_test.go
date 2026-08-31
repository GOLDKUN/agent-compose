package api

import (
	"errors"
	"testing"

	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func TestPumpExecAttachInputClosesRuntimeInputOnReceiveError(t *testing.T) {
	interaction := &execClosingRuntimeInteraction{}
	pumpExecAttachInput(func() (*agentcomposev2.AttachExecRequest, error) {
		return nil, errors.New("stream reset")
	}, interaction)
	if interaction.closeCalls != 1 {
		t.Fatalf("CloseSend calls = %d, want 1", interaction.closeCalls)
	}
}

type execClosingRuntimeInteraction struct {
	closeCalls int
}

func (*execClosingRuntimeInteraction) Send(driverpkg.RuntimeInputFrame) error { return nil }

func (i *execClosingRuntimeInteraction) CloseSend() error {
	i.closeCalls++
	return nil
}

func (*execClosingRuntimeInteraction) Recv() (driverpkg.RuntimeOutputFrame, error) {
	return driverpkg.RuntimeOutputFrame{}, errors.New("unused")
}

func (*execClosingRuntimeInteraction) Wait() (driverpkg.RuntimeResult, error) {
	return driverpkg.RuntimeResult{}, errors.New("unused")
}

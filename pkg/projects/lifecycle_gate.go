package projects

import (
	"context"
	"sync"
)

type projectLifecycleGates struct {
	mu    sync.Mutex
	gates map[string]*projectLifecycleGate
}

type projectLifecycleGate struct {
	token chan struct{}
	refs  int
}

func (g *projectLifecycleGates) acquire(ctx context.Context, name string) (func(), error) {
	g.mu.Lock()
	if g.gates == nil {
		g.gates = make(map[string]*projectLifecycleGate)
	}
	gate := g.gates[name]
	if gate == nil {
		gate = &projectLifecycleGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		g.gates[name] = gate
	}
	gate.refs++
	g.mu.Unlock()

	if err := ctx.Err(); err != nil {
		g.releaseRef(name, gate)
		return nil, err
	}
	select {
	case <-ctx.Done():
		g.releaseRef(name, gate)
		return nil, ctx.Err()
	case <-gate.token:
		return sync.OnceFunc(func() {
			gate.token <- struct{}{}
			g.releaseRef(name, gate)
		}), nil
	}
}

func (g *projectLifecycleGates) releaseRef(name string, gate *projectLifecycleGate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gate.refs--
	if gate.refs == 0 {
		delete(g.gates, name)
	}
}

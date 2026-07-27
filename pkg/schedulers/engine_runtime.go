package schedulers

import (
	"context"

	"github.com/fastschema/qjs"
)

func (e *QJSSchedulerEngine) execute(
	ctx context.Context,
	request SchedulerExecutionRequest,
	host SchedulerHost,
	validateOnly bool,
) (result SchedulerExecutionResult, err error) {
	return executeWithQJSPanicRecovery(ctx, func() (SchedulerExecutionResult, error) {
		return e.executeRuntime(ctx, request, host, validateOnly)
	})
}

func executeWithQJSPanicRecovery(
	ctx context.Context,
	execute func() (SchedulerExecutionResult, error),
) (result SchedulerExecutionResult, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		// CloseOnContextDone terminates the wazero module, while qjs v0.0.6
		// reports subsequent operations on that module as panics. Only translate
		// panics accompanied by this execution's cancellation; unrelated runtime
		// panics retain their existing fail-fast behavior.
		if cause := context.Cause(ctx); cause != nil {
			result = SchedulerExecutionResult{}
			err = cause
			return
		}
		panic(recovered)
	}()

	return execute()
}

func closeQJSLoaderRuntime(ctx context.Context, runtime *qjs.Runtime) {
	defer func() {
		// A canceled context may already have closed the module. qjs.Close then
		// attempts to free the QuickJS runtime through that closed module.
		if recovered := recover(); recovered != nil && context.Cause(ctx) == nil {
			panic(recovered)
		}
	}()
	runtime.Close()
}

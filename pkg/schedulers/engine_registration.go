package schedulers

import (
	"fmt"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

func (s *schedulerExecutionState) register(trigger domain.SchedulerTrigger, callback *qjs.Value) error {
	if _, ok := s.seenIDs[trigger.ID]; ok {
		return fmt.Errorf("duplicate scheduler trigger id %q", trigger.ID)
	}
	cloned := callback.Clone()
	s.seenIDs[trigger.ID] = struct{}{}
	s.registrations = append(s.registrations, schedulerRegistration{
		trigger:  trigger,
		callback: cloned,
		order:    len(s.registrations),
	})
	return nil
}

func (s *schedulerExecutionState) unregister(id string, kinds ...string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	allowedKinds := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		allowedKinds[strings.TrimSpace(kind)] = struct{}{}
	}
	next := make([]schedulerRegistration, 0, len(s.registrations))
	removed := false
	for _, item := range s.registrations {
		if item.trigger.ID == id {
			if len(allowedKinds) == 0 {
				removed = true
				item.callback = nil
				continue
			}
			if _, ok := allowedKinds[item.trigger.Kind]; ok {
				removed = true
				item.callback = nil
				continue
			}
		}
		item.order = len(next)
		next = append(next, item)
	}
	if removed {
		delete(s.seenIDs, id)
		s.registrations = next
	}
	return removed
}

func (s *schedulerExecutionState) freeCallbacks() {
	for i := range s.registrations {
		s.registrations[i].callback = nil
	}
}

func (s *schedulerExecutionState) triggers() []domain.SchedulerTrigger {
	items := make([]domain.SchedulerTrigger, 0, len(s.registrations))
	for _, item := range s.registrations {
		items = append(items, item.trigger)
	}
	return items
}

func parseIntervalRegistration(args []*qjs.Value) (string, int64, *qjs.Value, bool, error) {
	return parseTimerRegistration("scheduler.interval", "interval", args)
}

func parseTimeoutRegistration(args []*qjs.Value) (string, int64, *qjs.Value, bool, error) {
	return parseTimerRegistration("scheduler.timeout", "delay", args)
}

func parseTimerRegistration(name, delayLabel string, args []*qjs.Value) (string, int64, *qjs.Value, bool, error) {
	if len(args) < 2 {
		return "", 0, nil, false, fmt.Errorf("%s requires a callback and %s", name, delayLabel)
	}
	var id string
	var callback *qjs.Value
	var delayMs int64
	autoID := false
	switch {
	case args[0].IsFunction():
		callback = args[0]
		delayMs = args[1].Int64()
		if len(args) > 2 && args[2].IsString() {
			id = strings.TrimSpace(args[2].String())
		}
	case args[0].IsNumber() && args[1].IsFunction():
		delayMs = args[0].Int64()
		callback = args[1]
		if len(args) > 2 && args[2].IsString() {
			id = strings.TrimSpace(args[2].String())
		}
	case args[0].IsString() && len(args) > 2 && args[1].IsFunction():
		id = strings.TrimSpace(args[0].String())
		callback = args[1]
		delayMs = args[2].Int64()
	case args[0].IsString() && len(args) > 2 && args[2].IsFunction():
		id = strings.TrimSpace(args[0].String())
		delayMs = args[1].Int64()
		callback = args[2]
	default:
		return "", 0, nil, false, fmt.Errorf("unsupported %s signature", name)
	}
	if callback == nil || !callback.IsFunction() {
		return "", 0, nil, false, fmt.Errorf("%s requires a callback function", name)
	}
	if delayMs <= 0 {
		return "", 0, nil, false, fmt.Errorf("%s requires a positive %s", name, delayLabel)
	}
	if id == "" {
		autoID = true
	}
	return id, delayMs, callback, autoID, nil
}

func triggerHandle(value *qjs.Value) string {
	if value == nil || value.IsNull() || value.IsUndefined() {
		return ""
	}
	return strings.TrimSpace(value.String())
}

func parseCronRegistration(args []*qjs.Value) (string, string, *qjs.Value, string, bool, error) {
	if len(args) < 2 {
		return "", "", nil, "", false, fmt.Errorf("scheduler.cron requires an expression and callback")
	}
	var (
		expr     string
		id       string
		callback *qjs.Value
		timezone string
		err      error
	)
	switch {
	case args[0].IsString() && args[1].IsFunction():
		expr = strings.TrimSpace(args[0].String())
		callback = args[1]
		id, timezone, err = parseCronRegistrationOptions(args[2:]...)
	case len(args) > 2 && args[0].IsString() && args[1].IsString() && args[2].IsFunction():
		id = strings.TrimSpace(args[0].String())
		expr = strings.TrimSpace(args[1].String())
		callback = args[2]
		optionID, optionTimezone, optionErr := parseCronRegistrationOptions(args[3:]...)
		if optionErr != nil {
			err = optionErr
			break
		}
		if optionID != "" && optionID != id {
			err = fmt.Errorf("scheduler.cron received multiple trigger ids")
			break
		}
		timezone = optionTimezone
	default:
		return "", "", nil, "", false, fmt.Errorf("unsupported scheduler.cron signature")
	}
	if err != nil {
		return "", "", nil, "", false, err
	}
	if callback == nil || !callback.IsFunction() {
		return "", "", nil, "", false, fmt.Errorf("scheduler.cron requires a callback function")
	}
	if strings.TrimSpace(expr) == "" {
		return "", "", nil, "", false, fmt.Errorf("scheduler.cron requires a non-empty expression")
	}
	specJSON, err := schedulerCronSpecJSON(expr, timezone)
	if err != nil {
		return "", "", nil, "", false, err
	}
	autoID := strings.TrimSpace(id) == ""
	return expr, id, callback, specJSON, autoID, nil
}

func parseCronRegistrationOptions(args ...*qjs.Value) (string, string, error) {
	if len(args) == 0 {
		return "", "", nil
	}
	if len(args) > 1 {
		return "", "", fmt.Errorf("scheduler.cron accepts at most one options argument")
	}
	value := args[0]
	if value == nil || value.IsUndefined() || value.IsNull() {
		return "", "", nil
	}
	if value.IsString() {
		return strings.TrimSpace(value.String()), "", nil
	}
	options, err := qjs.ToGoValue[map[string]any](value)
	if err != nil {
		return "", "", fmt.Errorf("decode scheduler.cron options: %w", err)
	}
	var id string
	var timezone string
	if raw, ok := options["id"].(string); ok {
		id = strings.TrimSpace(raw)
	}
	if raw, ok := options["timezone"].(string); ok {
		timezone = strings.TrimSpace(raw)
	} else if raw, ok := options["tz"].(string); ok {
		timezone = strings.TrimSpace(raw)
	}
	return id, timezone, nil
}

func parseEventRegistration(args []*qjs.Value) (string, string, *qjs.Value, bool, error) {
	if len(args) < 2 {
		return "", "", nil, false, fmt.Errorf("scheduler.on requires a topic and callback")
	}
	topic := strings.TrimSpace(args[0].String())
	if topic == "" {
		return "", "", nil, false, fmt.Errorf("scheduler.on requires a non-empty topic")
	}
	var id string
	var callback *qjs.Value
	autoID := false
	if args[1].IsFunction() {
		callback = args[1]
		if len(args) > 2 && args[2].IsString() {
			id = strings.TrimSpace(args[2].String())
		}
	} else if len(args) > 2 && args[1].IsString() && args[2].IsFunction() {
		id = strings.TrimSpace(args[1].String())
		callback = args[2]
	} else {
		return "", "", nil, false, fmt.Errorf("unsupported scheduler.on signature")
	}
	if id == "" {
		autoID = true
	}
	return topic, id, callback, autoID, nil
}

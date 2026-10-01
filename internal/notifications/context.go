package notifications

import "context"

// How a change was triggered.
const (
	TriggerManual    = "manual"
	TriggerScheduler = "scheduler"
	TriggerApplyNow  = "apply_now"
	TriggerRevert    = "revert"
)

type changeContextKey struct{}

// ChangeContext carries who is making a DNS change and why.
type ChangeContext struct {
	Actor      *string
	ActorName  *string
	ActorEmail *string
	AuthType   *string
	Trigger    string
	ChangeID   *string
	ChangeName *string
	RequestID  *string
}

// WithChangeContext returns a child context carrying cc.
// Non-empty override fields replace those from any parent ChangeContext.
func WithChangeContext(ctx context.Context, overrides ChangeContext) context.Context {
	current := FromContext(ctx)
	merged := mergeChangeContext(current, overrides)
	return context.WithValue(ctx, changeContextKey{}, merged)
}

// FromContext returns the ChangeContext attached to ctx, or a zero value
// with TriggerManual when absent.
func FromContext(ctx context.Context) ChangeContext {
	if ctx == nil {
		return ChangeContext{Trigger: TriggerManual}
	}
	if v, ok := ctx.Value(changeContextKey{}).(ChangeContext); ok {
		return v
	}
	return ChangeContext{Trigger: TriggerManual}
}

// HasChangeContext reports whether ctx carries an explicitly set ChangeContext.
func HasChangeContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(changeContextKey{}).(ChangeContext)
	return ok
}

func mergeChangeContext(base, overrides ChangeContext) ChangeContext {
	out := base
	if overrides.Actor != nil {
		out.Actor = overrides.Actor
	}
	if overrides.ActorName != nil {
		out.ActorName = overrides.ActorName
	}
	if overrides.ActorEmail != nil {
		out.ActorEmail = overrides.ActorEmail
	}
	if overrides.AuthType != nil {
		out.AuthType = overrides.AuthType
	}
	if overrides.Trigger != "" {
		out.Trigger = overrides.Trigger
	}
	if overrides.ChangeID != nil {
		out.ChangeID = overrides.ChangeID
	}
	if overrides.ChangeName != nil {
		out.ChangeName = overrides.ChangeName
	}
	if overrides.RequestID != nil {
		out.RequestID = overrides.RequestID
	}
	if out.Trigger == "" {
		out.Trigger = TriggerManual
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

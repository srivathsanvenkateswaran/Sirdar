package httpapi

import "github.com/srivathsanvenkateswaran/sirdar/internal/app"

// The wire types are internal/app's, aliased so the handlers and their
// tests can name them without qualifying every one. internal/app owns the
// camelCase JSON tags that match desktop/frontend/src/api/types.ts; this
// package adds no shape of its own beyond the error envelope and the two
// small response bodies declared with the handlers that return them.
type (
	Workspace            = app.Workspace
	Usage                = app.Usage
	Budget               = app.Budget
	RunSummary           = app.RunSummary
	RunDetail            = app.RunDetail
	EventPayload         = app.EventPayload
	RunEvent             = app.RunEvent
	Ticket               = app.Ticket
	QuotaWindow          = app.QuotaWindow
	Quota                = app.Quota
	Check                = app.Check
	RegisterRow          = app.RegisterRow
	JobOutcome           = app.JobOutcome
	Event                = app.Event
	QueueFilter          = app.QueueFilter
	TriageOptions        = app.TriageOptions
	RCAOptions           = app.RCAOptions
	FixOptions           = app.FixOptions
	FixInfo              = app.FixInfo
	EvalOptions          = app.EvalOptions
	EvalReport           = app.EvalReport
	RetroReport          = app.RetroReport
	GoldenEntry          = app.GoldenEntry
	ConfigSummary        = app.ConfigSummary
	NotifySummary        = app.NotifySummary
	NotifyDestination    = app.NotifyDestination
	WebhooksSummary      = app.WebhooksSummary
	WebhookMatchSummary  = app.WebhookMatchSummary
	WebhookSourceSummary = app.WebhookSourceSummary
	JobID                = app.JobID
)

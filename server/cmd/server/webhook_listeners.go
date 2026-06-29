package main

import (
	"log/slog"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/outwebhook"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// registerWebhookListeners wires the outbound webhook dispatcher to the event
// bus. v1 listens only for issue status changes and POSTs a signed payload to
// every matching webhook_subscription (workspace-level or project-level).
//
// This mirrors registerAutopilotListeners: it filters issue:updated events on
// status_changed and reads the typed handler.IssueResponse out of the payload.
// Delivery itself is async (the dispatcher detaches each POST), so this listener
// never blocks the synchronous bus dispatch.
//
// Scope of issue.status_changed (v1): it fires for status transitions published
// as issue:updated with status_changed=true. That covers user/API/PR-merge
// changes (single update, batch update, PR-merged) AND the system-internal
// agent task-failure / stuck-issue auto-reset (in_progress → todo), which now
// publishes a status_changed event from TaskService.HandleFailedTasks. It does
// NOT fire for status mutations that never publish issue:updated with
// status_changed. The issue payload arrives in one of two shapes — the typed
// handler.IssueResponse (handler paths) or a map[string]any (service paths) —
// and both are handled below.
func registerWebhookListeners(bus *events.Bus, d *outwebhook.Dispatcher) {
	bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		statusChanged, _ := payload["status_changed"].(bool)
		if !statusChanged {
			return
		}
		fields, ok := webhookIssuePayload(payload["issue"])
		if !ok {
			slog.Debug("webhook listener: unrecognized issue payload shape")
			return
		}
		prevStatus, _ := payload["prev_status"].(string)

		d.DispatchIssueStatusChanged(outwebhook.IssueStatusChanged{
			WorkspaceID:    e.WorkspaceID,
			ProjectID:      fields.projectID,
			ActorType:      e.ActorType,
			ActorID:        e.ActorID,
			PreviousStatus: prevStatus,
			Issue:          fields.issue,
			Identifier:     fields.identifier,
			AssigneeType:   fields.assigneeType,
			AssigneeID:     fields.assigneeID,
		})
	})
}

// webhookIssueFields is everything the listener extracts from an issue:updated
// payload in one pass: the project id (project-level routing), the raw issue
// body to embed verbatim, and the identifier + polymorphic assignee the
// dispatcher needs to build issue_url + resolve the assignee name. The extraction
// lives here (not in outwebhook) because reading the typed handler.IssueResponse
// from outwebhook would be an import cycle (handler imports outwebhook).
type webhookIssueFields struct {
	projectID    string
	issue        any
	identifier   string
	assigneeType string
	assigneeID   string
}

// webhookIssuePayload extracts webhookIssueFields from either shape of the
// issue:updated payload: the typed handler.IssueResponse (handler paths) or the
// map[string]any emitted by service-layer status changes (e.g. issueToMap).
// Returns ok=false when neither shape is present. Missing fields come back as "".
func webhookIssuePayload(raw any) (webhookIssueFields, bool) {
	switch v := raw.(type) {
	case handler.IssueResponse:
		f := webhookIssueFields{issue: v, identifier: v.Identifier}
		if v.ProjectID != nil {
			f.projectID = *v.ProjectID
		}
		if v.AssigneeType != nil {
			f.assigneeType = *v.AssigneeType
		}
		if v.AssigneeID != nil {
			f.assigneeID = *v.AssigneeID
		}
		return f, true
	case map[string]any:
		f := webhookIssueFields{issue: v}
		f.projectID = stringFromMap(v["project_id"])
		f.identifier, _ = v["identifier"].(string)
		f.assigneeType = stringFromMap(v["assignee_type"])
		f.assigneeID = stringFromMap(v["assignee_id"])
		return f, true
	default:
		return webhookIssueFields{}, false
	}
}

// stringFromMap reads a string value from an issue map field that may be a
// string, a *string (nullable columns serialized by the service layer), or
// absent/nil.
func stringFromMap(raw any) string {
	switch s := raw.(type) {
	case string:
		return s
	case *string:
		if s != nil {
			return *s
		}
	}
	return ""
}

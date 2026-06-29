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
		projectID, issue, ok := webhookIssuePayload(payload["issue"])
		if !ok {
			slog.Debug("webhook listener: unrecognized issue payload shape")
			return
		}
		prevStatus, _ := payload["prev_status"].(string)
		identifier, assigneeType, assigneeID := webhookIssueEnrichFields(payload["issue"])

		d.DispatchIssueStatusChanged(outwebhook.IssueStatusChanged{
			WorkspaceID:    e.WorkspaceID,
			ProjectID:      projectID,
			ActorType:      e.ActorType,
			ActorID:        e.ActorID,
			PreviousStatus: prevStatus,
			Issue:          issue,
			Identifier:     identifier,
			AssigneeType:   assigneeType,
			AssigneeID:     assigneeID,
		})
	})
}

// webhookIssueEnrichFields pulls the identifier + polymorphic assignee
// (assignee_type / assignee_id) out of either issue payload shape so the
// dispatcher can build issue_url + resolve the assignee name without importing
// the handler package (which imports outwebhook — an import cycle). Missing
// fields come back as "".
func webhookIssueEnrichFields(raw any) (identifier, assigneeType, assigneeID string) {
	switch v := raw.(type) {
	case handler.IssueResponse:
		identifier = v.Identifier
		if v.AssigneeType != nil {
			assigneeType = *v.AssigneeType
		}
		if v.AssigneeID != nil {
			assigneeID = *v.AssigneeID
		}
	case map[string]any:
		identifier, _ = v["identifier"].(string)
		assigneeType = stringFromMap(v["assignee_type"])
		assigneeID = stringFromMap(v["assignee_id"])
	}
	return identifier, assigneeType, assigneeID
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

// webhookIssuePayload extracts the project id (for project-level routing) and
// the issue body to embed in the webhook, from either shape of the issue:updated
// payload: the typed handler.IssueResponse (handler paths) or the map[string]any
// emitted by service-layer status changes (e.g. issueToMap). Returns ok=false
// when neither shape is present.
func webhookIssuePayload(raw any) (projectID string, issue any, ok bool) {
	switch v := raw.(type) {
	case handler.IssueResponse:
		if v.ProjectID != nil {
			projectID = *v.ProjectID
		}
		return projectID, v, true
	case map[string]any:
		switch p := v["project_id"].(type) {
		case string:
			projectID = p
		case *string:
			if p != nil {
				projectID = *p
			}
		}
		return projectID, v, true
	default:
		return "", nil, false
	}
}

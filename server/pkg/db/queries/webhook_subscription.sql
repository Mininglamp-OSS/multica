-- =====================
-- Webhook subscriptions (outbound)
-- =====================
-- See migration 121. project_id IS NULL => workspace-level; otherwise
-- project-level. All reads/writes are scoped by workspace_id for multi-tenancy.

-- name: ListWebhookSubscriptionsByWorkspace :many
-- Workspace-level listing for the workspace settings UI: workspace-level
-- subscriptions only (project_id IS NULL).
SELECT * FROM webhook_subscription
WHERE workspace_id = $1 AND project_id IS NULL
ORDER BY created_at DESC;

-- name: ListWebhookSubscriptionsByProject :many
-- Project-level listing for the project settings UI.
SELECT * FROM webhook_subscription
WHERE workspace_id = $1 AND project_id = $2
ORDER BY created_at DESC;

-- name: ListEnabledWebhookSubscriptionsForDispatch :many
-- Dispatch hot path: every enabled subscription in the workspace. The
-- dispatcher filters workspace-level (project_id IS NULL) vs project-level
-- (project_id = issue's project) in memory against the event payload.
SELECT * FROM webhook_subscription
WHERE workspace_id = $1 AND enabled = true;

-- name: GetWebhookSubscriptionInWorkspace :one
SELECT * FROM webhook_subscription
WHERE id = $1 AND workspace_id = $2;

-- name: CreateWebhookSubscription :one
INSERT INTO webhook_subscription (
    workspace_id, project_id, url, secret, events, enabled
) VALUES (
    $1, sqlc.narg('project_id'), $2, $3, $4, $5
) RETURNING *;

-- name: UpdateWebhookSubscription :one
UPDATE webhook_subscription SET
    url = COALESCE(sqlc.narg('url'), url),
    events = COALESCE(sqlc.narg('events'), events),
    enabled = COALESCE(sqlc.narg('enabled'), enabled),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteWebhookSubscription :exec
DELETE FROM webhook_subscription
WHERE id = $1 AND workspace_id = $2;

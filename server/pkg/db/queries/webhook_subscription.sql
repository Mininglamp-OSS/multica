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
    -- Operator re-enabling a subscription clears the system-set disable marker
    -- and zeroes the failure counter, so the next failure window starts fresh
    -- rather than instantly tripping the auto-disable threshold again.
    disabled_reason = CASE
        WHEN sqlc.narg('enabled')::boolean IS TRUE THEN NULL
        ELSE disabled_reason
    END,
    consecutive_failures = CASE
        WHEN sqlc.narg('enabled')::boolean IS TRUE THEN 0
        ELSE consecutive_failures
    END,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteWebhookSubscription :exec
DELETE FROM webhook_subscription
WHERE id = $1 AND workspace_id = $2;

-- name: ResetWebhookSubscriptionFailures :exec
-- Called after a successful delivery. WHERE consecutive_failures > 0 keeps the
-- happy path (which always succeeds) from issuing a no-op UPDATE per delivery.
UPDATE webhook_subscription
SET consecutive_failures = 0
WHERE id = $1 AND consecutive_failures > 0;

-- name: IncrementWebhookSubscriptionFailuresAndMaybeDisable :one
-- Called after a terminal failed delivery. Increments the counter and, if the
-- new value crosses @threshold (set 0 to disable auto-disable), flips enabled
-- to false in the same UPDATE so we never publish an interim enabled+counter
-- state another transaction could race against.
--
-- Returns the post-update enabled flag + counter so the caller can log "the
-- threshold tripped" without an extra SELECT.
UPDATE webhook_subscription
SET
    consecutive_failures = consecutive_failures + 1,
    enabled = CASE
        WHEN @threshold::int > 0 AND consecutive_failures + 1 >= @threshold::int
            THEN false
        ELSE enabled
    END,
    disabled_reason = CASE
        WHEN @threshold::int > 0 AND consecutive_failures + 1 >= @threshold::int AND enabled
            THEN 'auto_disabled_failure_threshold'
        ELSE disabled_reason
    END,
    updated_at = now()
WHERE id = $1
RETURNING enabled, consecutive_failures, disabled_reason;

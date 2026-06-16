// Outbound webhook subscriptions: external HTTP endpoints Multica POSTs to when
// subscribed issue events fire. Modeled on GitHub org/repo webhooks —
// project_id null = workspace-level ("org"), project_id set = project-level
// ("repo"). See server migration 121 + handler/webhook_subscription.go.

// v1 emits a single event type. Kept as a string union (open to extension) so
// the UI can render new event types from the server without a code change.
export type WebhookSubscriptionEvent = "issue.status_changed";

export interface WebhookSubscription {
  id: string;
  workspace_id: string;
  // null for workspace-level subscriptions.
  project_id: string | null;
  url: string;
  events: string[];
  enabled: boolean;
  // Last 4 chars of the signing secret, to tell two secrets apart in the UI.
  secret_hint: string;
  // Full signing secret — present ONLY in the create response, shown once.
  secret?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateWebhookSubscriptionRequest {
  url: string;
  // Omit (or null) for a workspace-level webhook; set to scope to one project.
  project_id?: string | null;
  events?: string[];
}

export interface UpdateWebhookSubscriptionRequest {
  url?: string;
  events?: string[];
  enabled?: boolean;
}

export interface ListWebhookSubscriptionsResponse {
  subscriptions: WebhookSubscription[];
}

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

// Outbound webhook delivery history (server migration 122). One record per
// dispatcher deliver() call. Kept as a string union (open to extension) so a
// future status renders without a code change.
export type OutboundWebhookDeliveryStatus = "delivered" | "failed";

export interface OutboundWebhookDelivery {
  id: string;
  workspace_id: string;
  subscription_id: string;
  event: string;
  status: string;
  // Number of HTTP attempts the delivery made (1 = succeeded first try).
  attempt_count: number;
  // Last HTTP status; null when every attempt was a transport error.
  response_status: number | null;
  // Redacted last error (host-only), null on success.
  error: string | null;
  // Set when this row was produced by redelivering an earlier delivery.
  redelivered_from_id: string | null;
  created_at: string;
  // Detail-only (omitted from list responses): the exact signed payload we
  // sent and the truncated response body.
  request_body?: string | null;
  response_body?: string | null;
}

export interface ListOutboundWebhookDeliveriesResponse {
  deliveries: OutboundWebhookDelivery[];
  total: number;
}


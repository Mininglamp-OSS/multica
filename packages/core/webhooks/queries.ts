import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

// Query keys for outbound webhook subscriptions. Keyed on wsId so workspace
// switching swaps the cache automatically; project-scoped lists add the
// projectId segment. Delivery history keys on the subscription id.
export const webhookKeys = {
  all: (wsId: string) => ["webhook-subscriptions", wsId] as const,
  list: (wsId: string, projectId?: string) =>
    [...webhookKeys.all(wsId), projectId ?? "workspace"] as const,
  deliveries: (wsId: string, subscriptionId: string) =>
    [...webhookKeys.all(wsId), "deliveries", subscriptionId] as const,
  delivery: (wsId: string, subscriptionId: string, deliveryId: string) =>
    [...webhookKeys.deliveries(wsId, subscriptionId), deliveryId] as const,
};

// webhookSubscriptionsOptions lists subscriptions. With projectId it returns
// that project's webhooks; without it, workspace-level webhooks only.
export function webhookSubscriptionsOptions(wsId: string, projectId?: string) {
  return queryOptions({
    queryKey: webhookKeys.list(wsId, projectId),
    queryFn: () => api.listWebhookSubscriptions(projectId),
    select: (data) => data.subscriptions,
  });
}

// webhookDeliveriesOptions lists delivery history for one subscription
// (newest first, slim rows). enabled lets callers defer until a dialog opens.
export function webhookDeliveriesOptions(
  wsId: string,
  subscriptionId: string,
  opts?: { enabled?: boolean; limit?: number },
) {
  return queryOptions({
    queryKey: webhookKeys.deliveries(wsId, subscriptionId),
    queryFn: () =>
      api.listWebhookSubscriptionDeliveries(subscriptionId, {
        limit: opts?.limit ?? 50,
      }),
    enabled: opts?.enabled ?? true,
  });
}

// webhookDeliveryOptions fetches one delivery in full (request/response
// bodies), lazily — enabled only when a detail view is open.
export function webhookDeliveryOptions(
  wsId: string,
  subscriptionId: string,
  deliveryId: string,
  opts?: { enabled?: boolean },
) {
  return queryOptions({
    queryKey: webhookKeys.delivery(wsId, subscriptionId, deliveryId),
    queryFn: () =>
      api.getWebhookSubscriptionDelivery(subscriptionId, deliveryId),
    enabled: opts?.enabled ?? true,
  });
}

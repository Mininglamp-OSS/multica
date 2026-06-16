import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

// Query keys for outbound webhook subscriptions. Keyed on wsId so workspace
// switching swaps the cache automatically; project-scoped lists add the
// projectId segment.
export const webhookKeys = {
  all: (wsId: string) => ["webhook-subscriptions", wsId] as const,
  list: (wsId: string, projectId?: string) =>
    [...webhookKeys.all(wsId), projectId ?? "workspace"] as const,
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

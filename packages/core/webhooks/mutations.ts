import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { webhookKeys } from "./queries";
import { useWorkspaceId } from "../hooks";
import type {
  CreateWebhookSubscriptionRequest,
  UpdateWebhookSubscriptionRequest,
} from "../types";

// projectId scopes invalidation to the right list (workspace vs project). It is
// the same value passed to webhookSubscriptionsOptions on the calling surface.
export function useCreateWebhookSubscription(projectId?: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateWebhookSubscriptionRequest) =>
      api.createWebhookSubscription(data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: webhookKeys.list(wsId, projectId) });
    },
  });
}

export function useUpdateWebhookSubscription(projectId?: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateWebhookSubscriptionRequest) =>
      api.updateWebhookSubscription(id, data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: webhookKeys.list(wsId, projectId) });
    },
  });
}

export function useDeleteWebhookSubscription(projectId?: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteWebhookSubscription(id),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: webhookKeys.list(wsId, projectId) });
    },
  });
}

// useRedeliverWebhookSubscriptionDelivery re-POSTs a stored payload. The server
// enqueues it async (202) and records the new row only once the worker
// finishes, so invalidating once on settle can race ahead of that write. We
// invalidate immediately and again after a short delay so the new delivery row
// surfaces without a manual reload.
export function useRedeliverWebhookSubscriptionDelivery(subscriptionId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (deliveryId: string) =>
      api.redeliverWebhookSubscriptionDelivery(subscriptionId, deliveryId),
    onSettled: () => {
      const key = webhookKeys.deliveries(wsId, subscriptionId);
      qc.invalidateQueries({ queryKey: key });
      // The row is written after the 202 (and a failing endpoint retries for a
      // few seconds), so refetch again shortly to catch the recorded row.
      setTimeout(() => {
        void qc.invalidateQueries({ queryKey: key });
      }, 3000);
    },
  });
}

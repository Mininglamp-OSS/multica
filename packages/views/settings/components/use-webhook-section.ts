"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions } from "@multica/core/workspace/queries";
import { webhookSubscriptionsOptions } from "@multica/core/webhooks/queries";
import {
  useCreateWebhookSubscription,
  useDeleteWebhookSubscription,
  useUpdateWebhookSubscription,
} from "@multica/core/webhooks/mutations";
import type { WebhookSubscription } from "@multica/core/types";
import { useT } from "../../i18n";

// useWebhookSection owns all state + actions shared by the workspace-level
// settings tab and the project-level sidebar section. The two surfaces differ
// only in layout (and the workspace section fetches eagerly while the project
// section gates on `enabled`), so the logic lives here once and each component
// renders its own chrome. Toast copy uses the `settings` i18n namespace.
export interface UseWebhookSectionResult {
  canManage: boolean;
  subscriptions: WebhookSubscription[];
  newUrl: string;
  setNewUrl: (v: string) => void;
  createdSecret: string | null;
  setCreatedSecret: (v: string | null) => void;
  deleteTarget: WebhookSubscription | null;
  setDeleteTarget: (v: WebhookSubscription | null) => void;
  isCreating: boolean;
  isDeleting: boolean;
  handleCreate: () => Promise<void>;
  handleToggle: (sub: WebhookSubscription, enabled: boolean) => Promise<void>;
  handleDelete: () => Promise<void>;
  copySecret: (secret: string) => Promise<void>;
}

export function useWebhookSection(
  // projectId scopes the subscription list + create to one project (project-
  // level webhook). Omit for workspace-level.
  projectId?: string,
  // enabled lets a surface defer the query until visible (the sidebar section
  // gates on its open/collapsed state). Defaults to always-on.
  enabled = true,
): UseWebhookSectionResult {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage =
    currentMember?.role === "owner" || currentMember?.role === "admin";

  const { data: subscriptions = [] } = useQuery({
    ...webhookSubscriptionsOptions(wsId, projectId),
    enabled: !!wsId && canManage && enabled,
  });

  const createMutation = useCreateWebhookSubscription(projectId);
  const updateMutation = useUpdateWebhookSubscription(projectId);
  const deleteMutation = useDeleteWebhookSubscription(projectId);

  const [newUrl, setNewUrl] = useState("");
  // The signing secret is returned once on create; surfaced in a dialog so the
  // operator can copy it before it becomes unreachable.
  const [createdSecret, setCreatedSecret] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<WebhookSubscription | null>(
    null,
  );

  async function handleCreate() {
    const url = newUrl.trim();
    if (!url) return;
    try {
      const created = await createMutation.mutateAsync({
        url,
        project_id: projectId ?? null,
      });
      setNewUrl("");
      if (created.secret) setCreatedSecret(created.secret);
      toast.success(t(($) => $.webhooks.toast_created));
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.webhooks.toast_create_failed),
      );
    }
  }

  async function handleToggle(sub: WebhookSubscription, enabled_: boolean) {
    try {
      await updateMutation.mutateAsync({ id: sub.id, enabled: enabled_ });
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.webhooks.toast_update_failed),
      );
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await deleteMutation.mutateAsync(deleteTarget.id);
      setDeleteTarget(null);
      toast.success(t(($) => $.webhooks.toast_deleted));
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.webhooks.toast_delete_failed),
      );
    }
  }

  async function copySecret(secret: string) {
    try {
      await navigator.clipboard.writeText(secret);
      toast.success(t(($) => $.webhooks.toast_secret_copied));
    } catch {
      toast.error(t(($) => $.webhooks.toast_copy_failed));
    }
  }

  return {
    canManage,
    subscriptions,
    newUrl,
    setNewUrl,
    createdSecret,
    setCreatedSecret,
    deleteTarget,
    setDeleteTarget,
    isCreating: createMutation.isPending,
    isDeleting: deleteMutation.isPending,
    handleCreate,
    handleToggle,
    handleDelete,
    copySecret,
  };
}

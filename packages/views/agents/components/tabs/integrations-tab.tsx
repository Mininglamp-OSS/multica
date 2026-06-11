"use client";

import { useQuery } from "@tanstack/react-query";
import { Webhook, MessageSquare } from "lucide-react";
import type { Agent } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { larkInstallationsOptions } from "@multica/core/lark";
import { octoInstallationsOptions } from "@multica/core/octo";
import { memberListOptions } from "@multica/core/workspace/queries";
import { LarkAgentBindButton } from "../../../settings/components/lark-tab";
import { OctoAgentBindButton } from "../../../settings/components/octo-tab";
import { useT } from "../../../i18n";

/**
 * Integrations tab on the agent detail page. Surfaces the same external-
 * channel bind entry point as the inspector's "Integrations" section
 * (Lark Bot today) — scan-to-bind when unbound, connected info when bound —
 * but with the room a tab affords for a heading, a description, and the
 * not-configured / coming-soon / members-only states the cramped sidebar
 * section has no space for.
 *
 * The actionable affordance is the shared `LarkAgentBindButton`, the single
 * source of truth for "scan to bind vs. already connected". This tab only
 * adds the explanatory chrome around it, so the two entry points can never
 * drift.
 */
export function IntegrationsTab({ agent }: { agent: Agent }) {
  const { t } = useT("agents");
  const { t: ts } = useT("settings");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);

  // Both queries are already issued by LarkAgentBindButton (and keyed per
  // workspace), so re-reading them here is free — TanStack dedupes by key.
  // We only need the derived booleans to pick which copy sits next to the
  // button, mirroring the branch order LarkTab uses in Settings.
  const { data: listing } = useQuery({
    ...larkInstallationsOptions(wsId),
    enabled: !!wsId,
  });
  const { data: octoListing } = useQuery({
    ...octoInstallationsOptions(wsId),
    enabled: !!wsId,
  });
  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!wsId,
  });

  const configured = listing?.configured === true;
  const installSupported = listing?.install_supported === true;
  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage =
    currentMember?.role === "owner" || currentMember?.role === "admin";
  const hasActiveInstall =
    listing?.installations.some(
      (inst) => inst.agent_id === agent.id && inst.status === "active",
    ) ?? false;

  const octoConfigured = octoListing?.configured === true;

  return (
    <div className="space-y-6">
      <p className="text-xs text-muted-foreground">
        {t(($) => $.tab_body.integrations.intro)}
      </p>

      <section className="rounded-lg border">
        <div className="flex items-start gap-3 p-4">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground">
            <Webhook className="h-4 w-4" />
          </span>
          <div className="min-w-0 flex-1 space-y-1">
            <h3 className="text-sm font-medium">{ts(($) => $.lark.section_title)}</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              {ts(($) => $.lark.page_description)}
            </p>
          </div>
        </div>
        <div className="border-t px-4 py-3">
          {!configured ? (
            // No at-rest key on this deployment. The tab is only mounted
            // when the feature is configured, so this is the rare "key was
            // removed after an install existed" race.
            <p className="text-xs text-muted-foreground">
              {ts(($) => $.lark.not_enabled_title)}
            </p>
          ) : !canManage ? (
            // The backend gates install / manage on workspace owner/admin.
            // Members can still view connected bots in the (member-visible)
            // Settings listing, so point them there rather than show a dead
            // button.
            <p className="text-xs text-muted-foreground">
              {t(($) => $.tab_body.integrations.members_note)}
            </p>
          ) : !installSupported && !hasActiveInstall ? (
            // Key is set but the device-flow transport isn't wired in this
            // build — a fresh scan would fail at the post-poll bot-info step,
            // so we surface the "coming soon" notice instead of a broken CTA.
            // An agent that is ALREADY bound is exempt: install_supported only
            // governs NEW installs, so the bound state must still render below
            // (server/internal/handler/lark.go).
            <div className="space-y-1">
              <p className="text-xs font-medium">{ts(($) => $.lark.preview_title)}</p>
              <p className="text-xs text-muted-foreground">
                {ts(($) => $.lark.preview_description)}
              </p>
            </div>
          ) : (
            // Owner/admin with either a supported transport or an existing
            // bot: the shared button renders the scan-to-bind CTA or the
            // already-connected "Manage in Lark" badge.
            <LarkAgentBindButton agentId={agent.id} agentName={agent.name} />
          )}
        </div>
      </section>

      <section className="rounded-lg border">
        <div className="flex items-start gap-3 p-4">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground">
            <MessageSquare className="h-4 w-4" />
          </span>
          <div className="min-w-0 flex-1 space-y-1">
            <h3 className="text-sm font-medium">{ts(($) => $.octo.section_title)}</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              {ts(($) => $.octo.agent_bind_intro)}
            </p>
          </div>
        </div>
        <div className="border-t px-4 py-3">
          {!octoConfigured ? (
            <p className="text-xs text-muted-foreground">
              {ts(($) => $.octo.not_enabled_note)}
            </p>
          ) : !canManage ? (
            // The backend gates configure / disconnect on workspace owner/admin;
            // point members at the (member-visible) Settings listing instead of
            // a dead button.
            <p className="text-xs text-muted-foreground">
              {ts(($) => $.octo.members_note)}
            </p>
          ) : (
            // Owner/admin on an Octo-enabled deployment: the shared button
            // renders the Connect CTA or the already-connected status row.
            <OctoAgentBindButton agentId={agent.id} agentName={agent.name} />
          )}
        </div>
      </section>
    </div>
  );
}

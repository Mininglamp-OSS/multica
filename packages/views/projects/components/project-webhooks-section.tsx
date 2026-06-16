"use client";

import { useState } from "react";
import { ChevronRight, Plus, Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";
import { useWebhookSection } from "../../settings/components/use-webhook-section";
import { WebhookDialogs } from "../../settings/components/webhook-dialogs";

// Project-level outbound webhooks, rendered as a collapsible section in the
// project detail right panel (directly below Resources). Shares all logic with
// the workspace settings tab via useWebhookSection + WebhookDialogs; only the
// sidebar-compact layout and the open/adding toggles are local here.
export function ProjectWebhooksSection({ projectId }: { projectId: string }) {
  const { t } = useT("settings");
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);

  // Defer the subscription query until the section is expanded.
  const wh = useWebhookSection(projectId, open);

  async function handleCreate() {
    await wh.handleCreate();
    setAdding(false);
  }

  // Hidden entirely for members who can't manage webhooks — keeps the sidebar
  // free of an affordance they can't use (parity with the workspace tab, which
  // shows an admin-only notice on its own page).
  if (!wh.canManage) return null;

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        {t(($) => $.webhooks.section_header)}
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
        />
      </button>
      {open && (
        <div className="pl-2 space-y-1.5">
          {wh.subscriptions.length === 0 && !adding && (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.webhooks.empty)}
            </p>
          )}

          {wh.subscriptions.length > 0 && (
            <div className="max-h-64 space-y-1.5 overflow-y-auto pr-1">
              {wh.subscriptions.map((sub) => (
                <div
                  key={sub.id}
                  className="flex items-center gap-2 text-xs group"
                >
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <span className="truncate flex-1" title={sub.url}>
                          {sub.url}
                        </span>
                      }
                    />
                    <TooltipContent side="top">{sub.url}</TooltipContent>
                  </Tooltip>
                  <Switch
                    checked={sub.enabled}
                    onCheckedChange={(v) => wh.handleToggle(sub, v)}
                    aria-label={t(($) => $.webhooks.toggle_label)}
                  />
                  <button
                    type="button"
                    onClick={() => wh.setDeleteTarget(sub)}
                    className="opacity-0 group-hover:opacity-100 transition-opacity rounded-sm p-0.5 hover:bg-accent"
                    title={t(($) => $.webhooks.delete_label)}
                  >
                    <Trash2 className="size-3 text-muted-foreground" />
                  </button>
                </div>
              ))}
            </div>
          )}

          {adding ? (
            <div className="flex items-center gap-1.5">
              <input
                autoFocus
                type="url"
                value={wh.newUrl}
                onChange={(e) => wh.setNewUrl(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    void handleCreate();
                  } else if (e.key === "Escape") {
                    e.preventDefault();
                    setAdding(false);
                    wh.setNewUrl("");
                  }
                }}
                placeholder="https://example.com/webhooks/multica"
                className="flex-1 min-w-0 rounded-sm border bg-transparent px-2 py-1 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
              />
              <Button
                size="sm"
                variant="ghost"
                className="h-6 px-2 text-xs"
                disabled={!wh.newUrl.trim() || wh.isCreating}
                onClick={() => void handleCreate()}
              >
                {t(($) => $.webhooks.add_button)}
              </Button>
            </div>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs text-muted-foreground hover:text-foreground"
              onClick={() => setAdding(true)}
            >
              <Plus className="size-3" />
              {t(($) => $.webhooks.add_button)}
            </Button>
          )}

          <p className="px-2 text-[10px] text-muted-foreground">
            {t(($) => $.webhooks.event_hint)}
          </p>
        </div>
      )}

      <WebhookDialogs
        createdSecret={wh.createdSecret}
        setCreatedSecret={wh.setCreatedSecret}
        copySecret={wh.copySecret}
        deleteTarget={wh.deleteTarget}
        setDeleteTarget={wh.setDeleteTarget}
        handleDelete={wh.handleDelete}
        isDeleting={wh.isDeleting}
      />
    </div>
  );
}

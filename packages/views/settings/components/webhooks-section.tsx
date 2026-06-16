"use client";

import { Plus, Trash2, Webhook } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n";
import { useWebhookSection } from "./use-webhook-section";
import { WebhookDialogs } from "./webhook-dialogs";

// Workspace-level outbound webhooks (Settings → Webhooks tab). Logic lives in
// useWebhookSection; this component owns only the settings-card layout.
export function WebhooksSection() {
  const { t } = useT("settings");
  const wh = useWebhookSection();

  if (!wh.canManage) {
    return (
      <p className="text-sm text-muted-foreground">
        {t(($) => $.webhooks.admin_only)}
      </p>
    );
  }

  return (
    <div className="space-y-6">
      <section className="space-y-1">
        <p className="text-sm text-muted-foreground">
          {t(($) => $.webhooks.description_workspace)}
        </p>
      </section>

      {/* Add form */}
      <Card>
        <CardContent className="space-y-3">
          <Label htmlFor="webhook-url" className="text-sm font-medium">
            {t(($) => $.webhooks.add_label)}
          </Label>
          <div className="flex items-center gap-2">
            <Input
              id="webhook-url"
              type="url"
              placeholder="https://example.com/webhooks/multica"
              value={wh.newUrl}
              onChange={(e) => wh.setNewUrl(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") wh.handleCreate();
              }}
            />
            <Button
              onClick={wh.handleCreate}
              disabled={!wh.newUrl.trim() || wh.isCreating}
            >
              <Plus className="h-4 w-4" />
              {t(($) => $.webhooks.add_button)}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            {t(($) => $.webhooks.event_hint)}
          </p>
        </CardContent>
      </Card>

      {/* List */}
      {wh.subscriptions.length === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-md border border-dashed p-8 text-center">
          <Webhook className="h-6 w-6 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            {t(($) => $.webhooks.empty)}
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {wh.subscriptions.map((sub) => (
            <Card key={sub.id}>
              <CardContent className="flex items-center justify-between gap-4 py-3">
                <div className="min-w-0 space-y-1">
                  <p className="truncate text-sm font-medium" title={sub.url}>
                    {sub.url}
                  </p>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {sub.events.map((ev) => (
                      <Badge key={ev} variant="secondary" className="text-[10px]">
                        {ev}
                      </Badge>
                    ))}
                    {sub.secret_hint && (
                      <span className="text-xs text-muted-foreground">
                        {t(($) => $.webhooks.secret_hint, { hint: sub.secret_hint })}
                      </span>
                    )}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-3">
                  <Switch
                    checked={sub.enabled}
                    onCheckedChange={(v) => wh.handleToggle(sub, v)}
                    aria-label={t(($) => $.webhooks.toggle_label)}
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => wh.setDeleteTarget(sub)}
                    aria-label={t(($) => $.webhooks.delete_label)}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
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

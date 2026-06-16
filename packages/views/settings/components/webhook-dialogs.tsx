"use client";

import { Copy } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import type { WebhookSubscription } from "@multica/core/types";
import { useT } from "../../i18n";

// WebhookDialogs renders the two modals shared by every webhook surface: the
// secret-shown-once dialog (after create) and the delete confirmation. Driven
// entirely by useWebhookSection state so both the workspace tab and the project
// sidebar reuse identical UX.
export function WebhookDialogs({
  createdSecret,
  setCreatedSecret,
  copySecret,
  deleteTarget,
  setDeleteTarget,
  handleDelete,
  isDeleting,
}: {
  createdSecret: string | null;
  setCreatedSecret: (v: string | null) => void;
  copySecret: (secret: string) => void;
  deleteTarget: WebhookSubscription | null;
  setDeleteTarget: (v: WebhookSubscription | null) => void;
  handleDelete: () => void;
  isDeleting: boolean;
}) {
  const { t } = useT("settings");
  return (
    <>
      {/* Secret-once dialog */}
      <AlertDialog
        open={!!createdSecret}
        onOpenChange={(v) => {
          if (!v) setCreatedSecret(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.webhooks.secret_dialog_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.webhooks.secret_dialog_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {createdSecret && (
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1.5 text-xs">
                {createdSecret}
              </code>
              <Button
                variant="outline"
                size="icon"
                onClick={() => copySecret(createdSecret)}
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
          )}
          <AlertDialogFooter>
            <AlertDialogAction onClick={() => setCreatedSecret(null)}>
              {t(($) => $.webhooks.secret_dialog_done)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Delete confirm */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(v) => {
          if (!v && !isDeleting) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.webhooks.delete_confirm_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.webhooks.delete_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isDeleting}>
              {t(($) => $.webhooks.delete_confirm_cancel)}
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} disabled={isDeleting}>
              {t(($) => $.webhooks.delete_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

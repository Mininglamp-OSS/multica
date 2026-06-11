"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { OctoBindPage } from "@multica/views/octo";

// /octo/bind?token=<raw> is the bot's "you're not bound yet, click here"
// destination. Suspense wraps useSearchParams per Next.js 15's CSR-bailout
// rule; the loading text never paints in practice because the redemption
// page itself renders the "redeeming…" state immediately.
function OctoBindPageContent() {
  const searchParams = useSearchParams();
  const token = searchParams.get("token");
  return <OctoBindPage token={token} />;
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <OctoBindPageContent />
    </Suspense>
  );
}

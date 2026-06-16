import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import type { WebhookSubscription } from "@multica/core/types";

const mockCreate = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member";
const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const subsRef = vi.hoisted(() => ({
  current: [] as WebhookSubscription[],
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; select?: (d: unknown) => unknown }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current };
    if (key.includes("webhook")) return { data: subsRef.current };
    return { data: undefined };
  },
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
}));

vi.mock("@multica/core/webhooks/queries", () => ({
  webhookSubscriptionsOptions: () => ({
    queryKey: ["webhook-subscriptions"],
    queryFn: vi.fn(),
  }),
}));

vi.mock("@multica/core/webhooks/mutations", () => ({
  useCreateWebhookSubscription: () => ({ mutateAsync: mockCreate, isPending: false }),
  useUpdateWebhookSubscription: () => ({ mutateAsync: mockUpdate, isPending: false }),
  useDeleteWebhookSubscription: () => ({ mutateAsync: mockDelete, isPending: false }),
}));

vi.mock("@multica/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { WebhooksSection } from "./webhooks-section";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function makeSub(over: Partial<WebhookSubscription> = {}): WebhookSubscription {
  return {
    id: "sub-1",
    workspace_id: "workspace-1",
    project_id: null,
    url: "https://example.com/hook",
    events: ["issue.status_changed"],
    enabled: true,
    secret_hint: "ab12",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

describe("WebhooksSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    membersRef.current = [{ user_id: "user-1", role: "owner" }];
    subsRef.current = [];
  });

  it("shows the empty state when there are no subscriptions", () => {
    render(<WebhooksSection />, { wrapper: I18nWrapper });
    expect(screen.getByText(/No webhooks yet/i)).toBeTruthy();
  });

  it("lists existing subscriptions with their URL", () => {
    subsRef.current = [makeSub({ url: "https://hooks.acme.dev/in" })];
    render(<WebhooksSection />, { wrapper: I18nWrapper });
    expect(screen.getByText("https://hooks.acme.dev/in")).toBeTruthy();
  });

  it("creates a subscription from the add form", async () => {
    mockCreate.mockResolvedValue(makeSub({ secret: "whsec_revealed" }));
    render(<WebhooksSection />, { wrapper: I18nWrapper });

    const input = screen.getByPlaceholderText(/example\.com/i);
    await userEvent.type(input, "https://new.example.com/hook");
    await userEvent.click(screen.getByRole("button", { name: /^Add$/i }));

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith({
        url: "https://new.example.com/hook",
        project_id: null,
      }),
    );
    // Secret-once dialog surfaces the revealed secret.
    expect(await screen.findByText("whsec_revealed")).toBeTruthy();
  });

  it("hides management UI for non-admin members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    render(<WebhooksSection />, { wrapper: I18nWrapper });
    expect(screen.getByText(/Only workspace owners and admins/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Add$/i })).toBeNull();
  });
});

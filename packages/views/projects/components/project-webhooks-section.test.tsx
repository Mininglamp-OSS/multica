import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import enProjects from "../../locales/en/projects.json";
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
// Captures the projectId passed to webhookSubscriptionsOptions so we can assert
// the section scopes its query to the project.
const optionsProjectId = vi.hoisted(() => ({ current: undefined as string | undefined }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
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
  webhookSubscriptionsOptions: (_wsId: string, projectId?: string) => {
    optionsProjectId.current = projectId;
    return { queryKey: ["webhook-subscriptions", projectId], queryFn: vi.fn() };
  },
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

import { ProjectWebhooksSection } from "./project-webhooks-section";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings, projects: enProjects },
};

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
    project_id: "proj-1",
    url: "https://example.com/hook",
    events: ["issue.status_changed"],
    enabled: true,
    secret_hint: "ab12",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

const PROJECT_ID = "proj-1";

describe("ProjectWebhooksSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    membersRef.current = [{ user_id: "user-1", role: "owner" }];
    subsRef.current = [];
    optionsProjectId.current = undefined;
  });

  it("renders nothing for non-admin members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    const { container } = render(
      <ProjectWebhooksSection projectId={PROJECT_ID} />,
      { wrapper: I18nWrapper },
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("is collapsed by default and expands on click", async () => {
    render(<ProjectWebhooksSection projectId={PROJECT_ID} />, {
      wrapper: I18nWrapper,
    });
    // Header is always present; body (empty-state copy) only after expand.
    expect(screen.queryByText(/No webhooks yet/i)).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Webhooks/i }));
    expect(screen.getByText(/No webhooks yet/i)).toBeTruthy();
  });

  it("scopes the subscriptions query to the project", async () => {
    render(<ProjectWebhooksSection projectId={PROJECT_ID} />, {
      wrapper: I18nWrapper,
    });
    await userEvent.click(screen.getByRole("button", { name: /Webhooks/i }));
    expect(optionsProjectId.current).toBe(PROJECT_ID);
  });

  it("creates a project-scoped subscription with project_id", async () => {
    mockCreate.mockResolvedValue(makeSub({ secret: "whsec_revealed" }));
    render(<ProjectWebhooksSection projectId={PROJECT_ID} />, {
      wrapper: I18nWrapper,
    });
    await userEvent.click(screen.getByRole("button", { name: /Webhooks/i }));
    // Reveal the inline add input, then type + submit.
    await userEvent.click(screen.getByRole("button", { name: /^Add$/i }));
    await userEvent.type(
      screen.getByPlaceholderText(/example\.com/i),
      "https://p.example.com/hook",
    );
    await userEvent.click(screen.getByRole("button", { name: /^Add$/i }));

    await waitFor(() =>
      expect(mockCreate).toHaveBeenCalledWith({
        url: "https://p.example.com/hook",
        project_id: PROJECT_ID,
      }),
    );
    expect(await screen.findByText("whsec_revealed")).toBeTruthy();
  });

  it("lists existing project subscriptions when expanded", async () => {
    subsRef.current = [makeSub({ url: "https://hooks.acme.dev/proj" })];
    render(<ProjectWebhooksSection projectId={PROJECT_ID} />, {
      wrapper: I18nWrapper,
    });
    await userEvent.click(screen.getByRole("button", { name: /Webhooks/i }));
    expect(screen.getByText("https://hooks.acme.dev/proj")).toBeTruthy();
  });
});

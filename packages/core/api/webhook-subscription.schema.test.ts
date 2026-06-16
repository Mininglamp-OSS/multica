import { describe, expect, it } from "vitest";
import {
  WebhookSubscriptionResponseSchema,
  ListWebhookSubscriptionsResponseSchema,
  EMPTY_LIST_WEBHOOK_SUBSCRIPTIONS_RESPONSE,
  EMPTY_WEBHOOK_SUBSCRIPTION,
} from "./schemas";
import { parseWithFallback } from "./schema";

const baseSubscription = {
  id: "11111111-1111-1111-1111-111111111111",
  workspace_id: "ws-1",
  project_id: null,
  url: "https://example.com/hook",
  events: ["issue.status_changed"],
  enabled: true,
  secret_hint: "ab12",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("WebhookSubscription schemas", () => {
  it("parses a well-formed subscription", () => {
    const parsed = parseWithFallback(
      baseSubscription,
      WebhookSubscriptionResponseSchema,
      EMPTY_WEBHOOK_SUBSCRIPTION,
      { endpoint: "test" },
    );
    expect(parsed.url).toBe("https://example.com/hook");
    expect(parsed.events).toEqual(["issue.status_changed"]);
    expect(parsed.enabled).toBe(true);
  });

  it("keeps unknown server fields and unknown event types (lenient)", () => {
    const parsed = parseWithFallback(
      {
        subscriptions: [
          {
            ...baseSubscription,
            events: ["issue.status_changed", "issue.future_event"],
            future_field: "kept",
          },
        ],
      },
      ListWebhookSubscriptionsResponseSchema,
      EMPTY_LIST_WEBHOOK_SUBSCRIPTIONS_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.subscriptions).toHaveLength(1);
    expect(parsed.subscriptions[0]?.events).toContain("issue.future_event");
    expect(
      (parsed.subscriptions[0] as unknown as Record<string, unknown>).future_field,
    ).toBe("kept");
  });

  it("defaults a missing subscriptions array to empty", () => {
    const parsed = parseWithFallback(
      {},
      ListWebhookSubscriptionsResponseSchema,
      EMPTY_LIST_WEBHOOK_SUBSCRIPTIONS_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.subscriptions).toEqual([]);
  });

  it("falls back when subscriptions is the wrong type (null array)", () => {
    const parsed = parseWithFallback(
      { subscriptions: null },
      ListWebhookSubscriptionsResponseSchema,
      EMPTY_LIST_WEBHOOK_SUBSCRIPTIONS_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed).toEqual(EMPTY_LIST_WEBHOOK_SUBSCRIPTIONS_RESPONSE);
  });

  it("falls back to the empty subscription when a required field is the wrong type", () => {
    const parsed = parseWithFallback(
      { ...baseSubscription, url: 12345 },
      WebhookSubscriptionResponseSchema,
      EMPTY_WEBHOOK_SUBSCRIPTION,
      { endpoint: "test" },
    );
    expect(parsed).toEqual(EMPTY_WEBHOOK_SUBSCRIPTION);
  });
});

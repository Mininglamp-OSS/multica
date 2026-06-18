import { describe, expect, it } from "vitest";
import {
  OutboundWebhookDeliveryResponseSchema,
  ListOutboundWebhookDeliveriesResponseSchema,
  EMPTY_LIST_OUTBOUND_WEBHOOK_DELIVERIES_RESPONSE,
  EMPTY_OUTBOUND_WEBHOOK_DELIVERY,
} from "./schemas";
import { parseWithFallback } from "./schema";

const baseDelivery = {
  id: "11111111-1111-1111-1111-111111111111",
  workspace_id: "ws-1",
  subscription_id: "sub-1",
  event: "issue.status_changed",
  status: "delivered",
  attempt_count: 1,
  response_status: 200,
  error: null,
  redelivered_from_id: null,
  created_at: "2026-01-01T00:00:00Z",
};

describe("OutboundWebhookDelivery schemas", () => {
  it("parses a well-formed delivery", () => {
    const parsed = parseWithFallback(
      { ...baseDelivery, request_body: "{}", response_body: "ok" },
      OutboundWebhookDeliveryResponseSchema,
      EMPTY_OUTBOUND_WEBHOOK_DELIVERY,
      { endpoint: "test" },
    );
    expect(parsed.status).toBe("delivered");
    expect(parsed.attempt_count).toBe(1);
    expect(parsed.request_body).toBe("{}");
  });

  it("keeps unknown server fields and unknown status values (lenient)", () => {
    const parsed = parseWithFallback(
      {
        deliveries: [
          { ...baseDelivery, status: "future_status", future_field: "kept" },
        ],
        total: 1,
      },
      ListOutboundWebhookDeliveriesResponseSchema,
      EMPTY_LIST_OUTBOUND_WEBHOOK_DELIVERIES_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.deliveries).toHaveLength(1);
    expect(parsed.deliveries[0]?.status).toBe("future_status");
    expect(
      (parsed.deliveries[0] as unknown as Record<string, unknown>).future_field,
    ).toBe("kept");
  });

  it("defaults a missing deliveries array and total to empty", () => {
    const parsed = parseWithFallback(
      {},
      ListOutboundWebhookDeliveriesResponseSchema,
      EMPTY_LIST_OUTBOUND_WEBHOOK_DELIVERIES_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed.deliveries).toEqual([]);
    expect(parsed.total).toBe(0);
  });

  it("falls back when deliveries is the wrong type (null array)", () => {
    const parsed = parseWithFallback(
      { deliveries: null, total: 0 },
      ListOutboundWebhookDeliveriesResponseSchema,
      EMPTY_LIST_OUTBOUND_WEBHOOK_DELIVERIES_RESPONSE,
      { endpoint: "test" },
    );
    expect(parsed).toEqual(EMPTY_LIST_OUTBOUND_WEBHOOK_DELIVERIES_RESPONSE);
  });

  it("falls back to the empty delivery when a required field is the wrong type", () => {
    const parsed = parseWithFallback(
      { ...baseDelivery, id: 12345 },
      OutboundWebhookDeliveryResponseSchema,
      EMPTY_OUTBOUND_WEBHOOK_DELIVERY,
      { endpoint: "test" },
    );
    expect(parsed).toEqual(EMPTY_OUTBOUND_WEBHOOK_DELIVERY);
  });

  it("tolerates a null response_status (transport-error delivery)", () => {
    const parsed = parseWithFallback(
      { ...baseDelivery, status: "failed", response_status: null },
      OutboundWebhookDeliveryResponseSchema,
      EMPTY_OUTBOUND_WEBHOOK_DELIVERY,
      { endpoint: "test" },
    );
    expect(parsed.status).toBe("failed");
    expect(parsed.response_status).toBeNull();
  });
});

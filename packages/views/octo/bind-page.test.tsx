import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";

const TEST_RESOURCES = { en: { common: enCommon } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderWithI18n(ui: ReactElement) {
  return render(ui, { wrapper: I18nWrapper });
}

// ---------------------------------------------------------------------------
// Hoisted mocks
// ---------------------------------------------------------------------------

const mockRedeem = vi.hoisted(() => vi.fn());
const mockNavigate = vi.hoisted(() => vi.fn());

// Mutable auth state — mirrors the Lark bind-page test so individual cases can
// flip isLoading independently from user. The bind page reads both selectors;
// without isLoading the page used to flash "needs-auth" before the session
// resolved.
const mockAuthState = vi.hoisted(() => ({
  user: null as null | { id: string },
  isLoading: false,
}));

// Minimal ApiError stub carrying the status code — bind-page now classifies
// redemption failures by err.status, so the mock must expose the same class.
const ApiError = vi.hoisted(() => {
  return class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  };
});

vi.mock("@multica/core/api", () => ({
  api: { redeemOctoBindingToken: mockRedeem },
  ApiError,
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: typeof mockAuthState) => unknown) =>
      selector ? selector(mockAuthState) : mockAuthState,
    { getState: () => mockAuthState },
  ),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mockNavigate, replace: mockNavigate }),
}));

import { OctoBindPage } from "./bind-page";

describe("OctoBindPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockAuthState.user = { id: "u1" };
    mockAuthState.isLoading = false;
  });

  it("redeems the token and shows the bound state when logged in", async () => {
    mockRedeem.mockResolvedValue({
      workspace_id: "ws1",
      installation_id: "inst1",
      octo_uid: "uid1",
    });

    renderWithI18n(<OctoBindPage token="raw-token" />);

    await waitFor(() => {
      expect(screen.getByText(enCommon.octo_bind.done_title)).toBeInTheDocument();
    });
    expect(mockRedeem).toHaveBeenCalledWith("raw-token");
  });

  it("shows the missing-token error when no token is present", async () => {
    renderWithI18n(<OctoBindPage token={null} />);

    await waitFor(() => {
      expect(
        screen.getByText(enCommon.octo_bind.error_missing_token),
      ).toBeInTheDocument();
    });
    expect(mockRedeem).not.toHaveBeenCalled();
  });

  it("prompts for sign-in when the user is not authenticated", async () => {
    mockAuthState.user = null;
    renderWithI18n(<OctoBindPage token="raw-token" />);

    await waitFor(() => {
      expect(
        screen.getByText(enCommon.octo_bind.needs_auth_description),
      ).toBeInTheDocument();
    });
    // Must not attempt redemption without an identity (the redeemer is the
    // session, not the token).
    expect(mockRedeem).not.toHaveBeenCalled();
  });

  // Regression for the bug where the page flipped to "needs-auth" before
  // useAuthStore finished hydrating, leading an already-authenticated user
  // to click Sign in.
  it("shows redeeming text while auth is still loading (not needs-auth)", () => {
    mockAuthState.user = null;
    mockAuthState.isLoading = true;
    renderWithI18n(<OctoBindPage token="raw-token" />);
    expect(
      screen.getByText(enCommon.octo_bind.redeeming),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(enCommon.octo_bind.needs_auth_description),
    ).toBeNull();
  });

  // Regression for the param-name bug: the page used to push ?redirect=,
  // but the login page only honors ?next=. After Google OAuth round-trip
  // the user landed on the workspace dashboard instead of returning to
  // /octo/bind, so the binding never completed and the next Octo IM
  // message produced a fresh auth prompt — a permanent loop.
  it("sign-in button navigates with ?next= parameter (not ?redirect=)", () => {
    mockAuthState.user = null;
    mockAuthState.isLoading = false;
    renderWithI18n(<OctoBindPage token="mytoken" />);
    fireEvent.click(screen.getByText(enCommon.octo_bind.sign_in));
    expect(mockNavigate).toHaveBeenCalledTimes(1);
    const url = mockNavigate.mock.calls[0]?.[0] as string;
    expect(url).toContain("?next=");
    expect(url).not.toContain("?redirect=");
    expect(url).toContain(encodeURIComponent("mytoken"));
    // The destination encoded in ?next= must point back to /octo/bind so
    // the post-login redirect resumes the redemption flow.
    expect(url).toContain(encodeURIComponent("/octo/bind?token="));
  });

  it.each([
    [410, "octo_bind.error_expired"],
    [409, "octo_bind.error_already_bound"],
    [403, "octo_bind.error_not_member"],
  ])("maps backend HTTP %s to specific copy", async (status, key) => {
    mockRedeem.mockRejectedValue(new ApiError("redeem failed", status));
    renderWithI18n(<OctoBindPage token="raw-token" />);

    const expected =
      key === "octo_bind.error_expired"
        ? enCommon.octo_bind.error_expired
        : key === "octo_bind.error_already_bound"
          ? enCommon.octo_bind.error_already_bound
          : enCommon.octo_bind.error_not_member;

    await waitFor(() => {
      expect(screen.getByText(expected)).toBeInTheDocument();
    });
  });

  it("falls back to the generic error for an unexpected status", async () => {
    mockRedeem.mockRejectedValue(new ApiError("boom", 500));
    renderWithI18n(<OctoBindPage token="raw-token" />);
    await waitFor(() => {
      expect(screen.getByText(enCommon.octo_bind.error_unknown)).toBeInTheDocument();
    });
  });
});

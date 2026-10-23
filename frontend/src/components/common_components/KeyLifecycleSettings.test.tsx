import React, { useState } from "react";
import { Controller, useForm } from "react-hook-form";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "../../../tests/test-utils";
import { I18nProvider, translate } from "@/i18n";
import KeyLifecycleSettings from "./KeyLifecycleSettings";

const CREATE_PLACEHOLDER = "e.g., 30d or leave empty to never expire";
const EDIT_PLACEHOLDER = "e.g., 30d";

interface HarnessProps {
  isCreateMode?: boolean;
  onFinish?: (values: Record<string, unknown>) => void;
}

const Harness: React.FC<HarnessProps> = ({ isCreateMode = true, onFinish = () => {} }) => {
  const form = useForm<{ duration: string }>({ defaultValues: { duration: "" } });
  const [autoRotationEnabled, setAutoRotationEnabled] = useState(false);
  const [rotationInterval, setRotationInterval] = useState("");
  const [neverExpire, setNeverExpire] = useState(false);

  return (
    <form onSubmit={form.handleSubmit(onFinish)}>
      <Controller
        control={form.control}
        name="duration"
        render={({ field }) => (
          <KeyLifecycleSettings
            id={field.name}
            value={field.value}
            onChange={field.onChange}
            autoRotationEnabled={autoRotationEnabled}
            onAutoRotationChange={setAutoRotationEnabled}
            rotationInterval={rotationInterval}
            onRotationIntervalChange={setRotationInterval}
            isCreateMode={isCreateMode}
            neverExpire={neverExpire}
            onNeverExpireChange={setNeverExpire}
          />
        )}
      />
      <button type="submit">submit</button>
      <button type="button" onClick={() => form.reset()}>
        reset
      </button>
      <span data-testid="rotation-interval-value">{rotationInterval}</span>
    </form>
  );
};

const getDurationInput = (isCreateMode = true) =>
  screen.getByPlaceholderText(isCreateMode ? CREATE_PLACEHOLDER : EDIT_PLACEHOLDER) as HTMLInputElement;

describe("KeyLifecycleSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the expiry and auto-rotation sections", () => {
    renderWithProviders(<Harness />);
    expect(screen.getByText("Key Expiry Settings")).toBeInTheDocument();
    expect(screen.getByText(/Automatic rotation is not supported/)).toBeInTheDocument();
    expect(getDurationInput()).toBeInTheDocument();
  });

  it("gives the duration input and the Never Expire checkbox their own labels", () => {
    renderWithProviders(<Harness isCreateMode={false} />);
    expect(screen.getByLabelText("Expire Key")).toBe(getDurationInput(false));
    expect(screen.getByRole("checkbox", { name: "Never Expire" })).toBeInTheDocument();
  });

  it("uses the create-mode placeholder in create mode", () => {
    renderWithProviders(<Harness isCreateMode={true} />);
    expect(screen.getByPlaceholderText(CREATE_PLACEHOLDER)).toBeInTheDocument();
  });

  it("uses the edit-mode placeholder in edit mode", () => {
    renderWithProviders(<Harness isCreateMode={false} />);
    expect(screen.getByPlaceholderText(EDIT_PLACEHOLDER)).toBeInTheDocument();
  });

  describe("duration is a single source of truth (regression for pre-filled value dropped on submit)", () => {
    it("submits the duration the user typed", async () => {
      const user = userEvent.setup();
      const onFinish = vi.fn();
      renderWithProviders(<Harness onFinish={onFinish} />);

      fireEvent.change(getDurationInput(), { target: { value: "1d" } });
      await user.click(screen.getByRole("button", { name: "submit" }));

      await waitFor(() => expect(onFinish).toHaveBeenCalledTimes(1));
      expect(onFinish.mock.calls[0][0]).toMatchObject({ duration: "1d" });
    });

    it("clears the displayed value when the form is reset, so no stale value lingers", async () => {
      const user = userEvent.setup();
      renderWithProviders(<Harness />);

      fireEvent.change(getDurationInput(), { target: { value: "1d" } });
      expect(getDurationInput().value).toBe("1d");

      await user.click(screen.getByRole("button", { name: "reset" }));

      await waitFor(() => expect(getDurationInput().value).toBe(""));
    });

    it("never submits a value that differs from what is displayed after a reset", async () => {
      const user = userEvent.setup();
      const onFinish = vi.fn();
      renderWithProviders(<Harness onFinish={onFinish} />);

      // First create: type "1d" and submit -> "1d" is sent.
      fireEvent.change(getDurationInput(), { target: { value: "1d" } });
      await user.click(screen.getByRole("button", { name: "submit" }));
      await waitFor(() => expect(onFinish).toHaveBeenCalledTimes(1));
      expect(onFinish.mock.calls[0][0]).toMatchObject({ duration: "1d" });

      // Second create: form resets, so the field must show empty AND submit empty.
      // The old bug showed a stale "1d" while submitting null/empty.
      await user.click(screen.getByRole("button", { name: "reset" }));
      await waitFor(() => expect(getDurationInput().value).toBe(""));

      await user.click(screen.getByRole("button", { name: "submit" }));
      await waitFor(() => expect(onFinish).toHaveBeenCalledTimes(2));
      expect(onFinish.mock.calls[1][0].duration).not.toBe("1d");
      expect(getDurationInput().value).toBe(onFinish.mock.calls[1][0].duration ?? "");
    });
  });

  describe("Never Expire", () => {
    it("clears and disables the duration input, then submits an empty duration", async () => {
      const user = userEvent.setup();
      const onFinish = vi.fn();
      renderWithProviders(<Harness isCreateMode={false} onFinish={onFinish} />);

      fireEvent.change(getDurationInput(false), { target: { value: "30d" } });
      expect(getDurationInput(false).value).toBe("30d");

      await user.click(screen.getByRole("checkbox", { name: /never expire/i }));

      await waitFor(() => expect(getDurationInput(false).value).toBe(""));
      expect(getDurationInput(false)).toBeDisabled();

      await user.click(screen.getByRole("button", { name: "submit" }));
      await waitFor(() => expect(onFinish).toHaveBeenCalledTimes(1));
      expect(onFinish.mock.calls[0][0]).toMatchObject({ duration: "" });
    });
  });
});
/** 验证真实共享生命周期控件；前置双语言且旧调用方仍带轮换标志，确认仅展示真实期限能力与不支持说明，DOM 自动清理。 */
describe("KeyLifecycleSettings supported capabilities", () => {
  it.each(["en", "zh-CN"] as const)("shows expiry and an explicit unsupported notice in %s", (locale) => {
    const onChange = vi.fn();
    renderWithProviders(
      <I18nProvider initialLocale={locale}>
        <KeyLifecycleSettings
          value="30d"
          onChange={onChange}
          autoRotationEnabled={true}
          onAutoRotationChange={vi.fn()}
          rotationInterval="1d"
          onRotationIntervalChange={vi.fn()}
        />
      </I18nProvider>,
    );
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    expect(screen.getByText(translate(locale, "keyEditor.rotationUnsupported"))).toBeVisible();
    const input = screen.getByLabelText(translate(locale, "keyEditor.expire"));
    expect(input).toHaveValue("30d");
    fireEvent.change(input, { target: { value: "invalid user text" } });
    expect(onChange).toHaveBeenCalledWith("invalid user text");
  });
});

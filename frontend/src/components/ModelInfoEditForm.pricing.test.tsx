import { expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { renderWithProviders, screen } from "../../tests/test-utils";
import { I18nProvider, LOCALE_COOKIE } from "@/i18n";
import { translate } from "@/i18n/translate";
import ModelInfoEditForm from "./ModelInfoEditForm";

/** 前置手动Token模型，验证旧编辑器缺价提示与显式0，保留留空语义；真实表单渲染，自动卸载，无后台写入。 */
for (const locale of ["en", "zh-CN"] as const) {
  it("legacy editor explains cache price absence " + locale, async () => {
    localStorage.removeItem(LOCALE_COOKIE);
    document.cookie = `${LOCALE_COOKIE}=; path=/; max-age=0`;
    const model = {
      model_name: "pricing-fixture",
      litellm_params: { model: "pricing-fixture", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
      model_info: { id: "fixture", pricing_source: "manual" },
    };
    renderWithProviders(
      <I18nProvider initialLocale={locale}>
        <ModelInfoEditForm
          localModelData={model}
          modelData={model}
          teamAlias={null}
          accessToken={null}
          isEditing
          isSaving={false}
          isWildcardModel={false}
          ptuCostAttributionEnabled={false}
          showCacheControl={false}
          setShowCacheControl={vi.fn()}
          onCancel={vi.fn()}
          onSubmit={async () => {}}
          modelAccessGroups={[]}
          guardrailsList={[]}
          tagsList={{}}
          credentialsList={[]}
          healthCheckModelOptions={[]}
        />
      </I18nProvider>,
    );
    const tr = (key: string) => translate(locale, key);
    await expect(screen.getByRole("status", { name: tr("cachePrice.warningTitle") })).toBeVisible();
    await userEvent.type(screen.getByLabelText(tr("Cache Read Cost (per 1M tokens)")), "0");
    await userEvent.type(screen.getByLabelText(tr("Cache Write Cost (per 1M tokens)")), "0");
    expect(screen.queryByRole("status", { name: tr("cachePrice.warningTitle") })).toBeNull();
    expect(screen.queryByPlaceholderText(tr("Defaults to Input Cost if blank"))).toBeNull();
  });
}

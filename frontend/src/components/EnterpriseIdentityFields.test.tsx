import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { I18nProvider, translate, useI18n } from "@/i18n";
import { EnterpriseIdentityFields } from "./EnterpriseIdentityFields";
vi.mock("@/lib/http/enterprise", () => ({ listSSO: vi.fn() }));
afterEach(() => { cleanup(); localStorage.clear(); document.cookie = "xhub_locale=; max-age=0; path=/"; });
/** 双语控件宿主；保存企业原始 ID，通过语言按钮切换验证文案和用户内容；不调用后台，无持久化资源。 */
function Form() {
 const { setLocale } = useI18n(); const [externalID,setExternalID] = useState("Employee原始ID");
 return <><button onClick={() => setLocale("zh-CN")}>Switch language</button><EnterpriseIdentityFields connections={[]} connectionID="connection" externalID={externalID} onConnectionChange={() => {}} onExternalChange={setExternalID} error /></>;
}
// 英文切中文验证错误、字段和原始内容；DOM 与存储由 afterEach 清理，不依赖 IdP。
it("switches identity fields and errors without translating user input", async () => {
 const user = userEvent.setup(); render(<I18nProvider initialLocale="en"><Form /></I18nProvider>);
 expect(screen.getByRole("alert")).toHaveTextContent(translate("en","enterpriseUserIdentity.loadError"));
 expect(screen.getByLabelText(translate("en","enterpriseUserIdentity.externalID"))).toHaveValue("Employee原始ID");
 await user.click(screen.getByRole("button",{name:"Switch language"}));
 expect(screen.getByRole("alert")).toHaveTextContent(translate("zh-CN","enterpriseUserIdentity.loadError"));
 expect(screen.getByLabelText(translate("zh-CN","enterpriseUserIdentity.externalID"))).toHaveValue("Employee原始ID");
});
// 目录托管身份只能读；验证可见提示及禁用控件，afterEach 回收 DOM。
it.each(["en","zh-CN"] as const)("keeps managed identities read only in %s", locale => {
 render(<I18nProvider initialLocale={locale}><EnterpriseIdentityFields connections={[]} connectionID="managed" externalID="employee" onConnectionChange={vi.fn()} onExternalChange={vi.fn()} managed /></I18nProvider>);
 expect(screen.getByText(translate(locale,"enterpriseUserIdentity.managed"))).toBeVisible();
 expect(screen.getByRole("combobox")).toBeDisabled(); expect(screen.getByRole("textbox")).toBeDisabled();
});

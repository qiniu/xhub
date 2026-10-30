import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, stableGoto } from "./helpers";

/** 注册真实公共模型页的Skill失败展示及恢复对话流程；locale指定语言，真实网关返回404而不是浏览器拦截。
 * 检查失败不伪装空列表、支持提示、没有安装命令和写请求；随后真实模型请求抵达本地上游。
 * 不创建Skill配置，运行器负责测试会话和schema清理；不调用生产或外部插件仓库。
 */
export function registerSkillHubSupport(locale: Locale) {
  test("skill hub reports unsupported discovery and preserves chat " + locale, async ({ page }) => {
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const discovery = await page.request.get(GATEWAY + "/public/skill_hub");
    expect(discovery.status()).toBe(404);
    const writes: string[] = [];
    page.on("request", req => {
      if (req.method() !== "GET" && new URL(req.url()).pathname.includes("claude-code")) writes.push(req.url());
    });
    const failedRequest = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/public/skill_hub"));
    await stableGoto(page, "/model_hub");
    expect((await failedRequest).status()).toBe(404);
    await page.getByRole("tab", { name: tr("Skill Hub"), exact: true }).click();
    await expect(page.getByRole("tabpanel").getByRole("alert")).toHaveText(tr("skillHub.loadError"));
    await expect(page.getByRole("heading", { name: tr("skillHub.supportTitle") })).toBeVisible();
    await expect(page.getByText(tr("skillHub.empty"), { exact: true })).toHaveCount(0);
    await expect(page.getByRole("tabpanel")).not.toContainText("/plugin");
    expect(writes).toEqual([]);
    await stableGoto(page, "/playground");
    await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
    await page.getByRole("option", { name: "gpt-4o-mini", exact: true }).click();
    await page.getByPlaceholder(tr("Select an endpoint"), { exact: true }).click();
    await page.getByRole("option").filter({ hasText: /(?:^|\s)\/v1\/chat\/completions$/ }).click();
    const before = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(before.status()).toBe(200);
    const count = (await before.json()).requests;
    const prompt = "Verify the Skill support notice does not prevent this conversation.";
    await page.getByTestId("chat-composer-input").fill(prompt);
    const inference = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/chat/completions") && response.request().method() === "POST");
    await page.getByTestId("chat-send-button").click();
    expect((await inference).status()).toBe(200);
    await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
    await expect(page.getByTestId("message-surface").filter({ hasText: prompt })).toBeVisible();
    const after = await page.request.get(UPSTREAM + "/e2e/request-count");
    expect(after.status()).toBe(200);
    expect((await after.json()).requests).toBe(count + 1);
  });
}

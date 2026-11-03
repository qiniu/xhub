import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";
/** 用途：注册独立场景「market refresh preserves local records and listing state」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC111() {


test("market refresh preserves local records and listing state", async ({ page }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  // 旧删除别名不能偷偷改变目录，当前页面只能通过明确上下架状态操作。
  const retired = await page.request.delete(GATEWAY + "/price/model", { headers, data: { id: "e2e-market-chat" } });
  expect(retired.status(), "废弃价格删除入口必须不可用").toBe(404);
  await stableGoto(page, "/price-data");
  await expect(page.getByRole("tab", { name: "模型供应商", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: t("Price Data Management"), exact: true }).click();
  const management = page.getByRole("dialog", { name: t("Price Data Management"), exact: true });
  const reload = page.waitForResponse(
    (r) => new URL(r.url()).pathname.endsWith("/reload/model_cost_map") && r.request().method() === "POST",
  );
  // 在确认操作之前订阅响应，兼容通过 /gw 前缀的同源代理请求。
  await management.getByRole("button", { name: t("Reload Price Data"), exact: true }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: t("Yes"), exact: true }).click();
  expect((await reload).status()).toBe(200);
  const prompt = page.getByRole("dialog", { name: "是否添加价格表中的模型？" });
  await expect(prompt.getByRole("status")).toHaveText("发现 2 个尚未部署的在售公开模型。");
  await expect(prompt.getByRole("link", { name: "添加此模型" })).toHaveCount(2);
  await prompt.getByRole("button", { name: "暂不添加" }).click();
  await expect(page.getByTestId("price-row-e2e-market-chat")).toBeVisible();
  await expect(page.getByTestId("price-row-e2e-market-private")).toHaveCount(0);
  await expect(page.getByTestId("price-row-e2e-market-retired")).toHaveCount(0);
  const features = page.getByRole("group", { name: "特性标签" });
  await features.getByRole("button", { name: "视频生成 1", exact: true }).click();
  await expect(page.getByTestId("price-row-e2e-market-video")).toBeVisible();
  await expect(page.getByTestId("price-row-e2e-market-chat")).toHaveCount(0);
  await page.getByRole("button", { name: t("priceCatalog.reset"), exact: true }).click();
  await page.getByRole("button", { name: "查看 e2e-market-chat 详情" }).click();
  const detail = page.getByRole("dialog", { name: "e2e-market-chat", exact: true });
  await expect(detail.getByRole("table", { name: "完整市场价格" })).toContainText("$2");
  await detail.getByRole("tab", { name: "接入信息", exact: true }).click();
  await expect(detail.getByText(/尚未部署此模型/)).toBeVisible();
  await detail.getByRole("tab", { name: "API 接入", exact: true }).click();
  await expect(detail.getByText(/XHUB_API_KEY 环境变量/)).toBeVisible();
  // 模拟旧部署只保存标准接口，目录仍需显示已自动开放的匹配透传入口。
  const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
    model_name: "e2e-market-automatic-bypass",
    litellm_params: { model: "e2e-market-chat", custom_llm_provider: "custom", api_base: UPSTREAM, api_key: "sk-fake" },
    model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "catalog", base_model: "e2e-market-chat" },
  } });
  expect(created.status(), await created.text()).toBe(200);
  const modelID = (await created.json()).model_info.id;
  try {
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "查看 e2e-market-chat 详情" }).click();
    await detail.getByRole("tab", { name: "API 接入", exact: true }).click();
    await expect(detail.getByText("/bypass/openai/v1/chat/completions", { exact: true })).toBeVisible();
    const response = await page.request.post(GATEWAY + "/bypass/openai/v1/chat/completions", { headers, data: {
      model: "e2e-market-automatic-bypass", messages: [{ role: "user", content: "Hello" }],
    } });
    expect(response.status(), await response.text()).toBe(200);
  } finally {
    const removed = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: modelID } });
    expect(removed.status(), await removed.text()).toBe(200);
  }
  await detail.getByRole("tab", { name: "调用文档", exact: true }).click();
  await expect(detail.getByText(/内部调用流程/)).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByTestId("price-row-e2e-market-chat").getByRole("button", { name: "下架", exact: true }).click();
  await expect(page.getByTestId("price-row-e2e-market-chat")).toHaveCount(0);
  await page.getByRole("tab", { name: "本地模型列表", exact: true }).click();
  const local = page.getByTestId("local-model-e2e-market-chat");
  await expect(local).toContainText("已下架");
  await expect(page.getByTestId("local-model-e2e-market-private")).toContainText("非公开");
  const refreshed = await page.request.post(GATEWAY + "/reload/model_cost_map", { headers });
  expect(refreshed.status(), await refreshed.text()).toBe(200);
  await page.reload();
  await page.getByRole("tab", { name: "本地模型列表", exact: true }).click();
  await expect(local).toContainText("已下架");
  const saved = await page.request.get(GATEWAY + "/price/catalog", { headers });
  const row = (await saved.json()).models.find((r: { id: string }) => r.id === "e2e-market-chat");
  expect(row).toMatchObject({ delisted: true, pricing_rules_v2: expect.any(Array) });
  await local.getByRole("button", { name: "上架", exact: true }).click();
  await expect(local).toContainText("未下架");
  await page.getByRole("tab", { name: "模型广场", exact: true }).click();
  await expect(page.getByTestId("price-row-e2e-market-chat")).toBeVisible();
  const plan = await page.request.post(GATEWAY + "/schedule/model_cost_map_reload?hours=6", { headers });
  expect(plan.status()).toBe(200);
  expect(
    (await (await page.request.get(GATEWAY + "/schedule/model_cost_map_reload/status", { headers })).json()).scheduled,
  ).toBe(true);
  expect((await page.request.delete(GATEWAY + "/schedule/model_cost_map_reload", { headers })).status()).toBe(200);
});
}

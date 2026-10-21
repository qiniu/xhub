/** 目的：独立验证 /model_hub 的页面与读接口；前置隔离网关与管理员，异常独立报告；上下文和 schema 由运行器清理。 */
import { discoverPages } from "../../routes";
import { registerPageScenario } from "../../page-scenario";
registerPageScenario(discoverPages().find(page => page.route === '/model_hub')!, '/model_hub');

import { test, expect } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "../../helpers";

// 通过真实配置接口保存自定义链接名，验证页面切换语言后名称不改写；finally 恢复原配置，隔离数据库由运行器清理。
test("public hub preserves configured names across languages", async ({page}) => {
  await loginAdmin(page);
  const headers = {Authorization: "Bearer " + await sessionBearer(page)};
  const info = await page.request.get(GATEWAY + "/public/model_hub/info");
  expect(info.status()).toBe(200);
  const old = await info.json();
  const name = "LiteLLM 模型 anthropic/claude-opus-5.5";
  try {
    const saved = await page.request.post(GATEWAY + "/model_hub/update_useful_links", {headers,data:{useful_links:{[name]:"https://example.com/guide"}}});
    expect(saved.status()).toBe(200);
    await stableGoto(page,"/model_hub");
    await expect(page.getByRole("button",{name,exact:true})).toBeVisible();
    await stableGoto(page,"/api-keys");
    await page.getByRole("button",{name:"English",exact:true}).click();
    await stableGoto(page,"/model_hub");
    await expect(page.getByRole("button",{name,exact:true})).toBeVisible();
  } finally {
    expect((await page.request.post(GATEWAY + "/model_hub/update_useful_links",{headers,data:{useful_links:old.useful_links}})).status()).toBe(200);
  }
});

import { requireAuthorizationError } from "./assertions";
import { chooseKeyTeam, chooseOrganization } from "./helpers";

import { GATEWAY, UPSTREAM } from "./helpers";

import { expect, test, type Page } from "@playwright/test";

import { loginAdmin, sessionBearer, stableGoto, t, uiPath, watchGateway } from "./helpers";


/** 从真实登录 cookie 读取会话；page 为浏览器，返回 bearer，供旧场景操作自建资源，缺凭据时失败。 */
async function sessionFrom(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const token = cookies.find((c) => c.name === "token")?.value;
  return JSON.parse(Buffer.from(token!.split(".")[1], "base64url").toString()).key as string;
}
/** 注册密钥单项生命周期；kind 指定编辑、封禁、解封或删除，title 为报告名称，返回无。
 * 前置页面创建专属个人密钥；独立验证页面、持久化和真实推理，拒绝核对 401 契约与无上游调用；finally 删除并回读 404。 */
export function registerKeyWrite(kind: "edit" | "block" | "unblock" | "delete", title: string) {
  test(title, async ({page}) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const headers = {Authorization: "Bearer " + await sessionBearer(page)};
    const alias = "e2e-key-" + kind + "-" + Date.now();
    await page.getByTestId("create-key-button").click(); await chooseKeyTeam(page);
    await page.getByLabel(t("Key Name")).fill(alias);
    const pending = page.waitForResponse(r => new URL(r.url()).pathname === "/key/generate" && r.request().method() === "POST");
    await page.getByRole("button", {name:t("pages.apiKeys.createSubmit"), exact:true}).click();
    const created = await pending; expect(created.status(), await created.text()).toBe(200);
    const key = await created.json();
    /** 调用真实推理；返回 HTTP 响应，凭据来自页面，测试账单由私有 schema 回收。 */
    const invoke = () => page.request.post(GATEWAY + "/v1/chat/completions", {
      headers: {Authorization: "Bearer " + key.key},
      data: {model:"gpt-4o-mini", messages:[{role:"user", content:"Verify key " + kind}]},
    });
    /** 回读当前密钥；返回 info，读失败立即失败，无写入副作用。 */
    const info = async () => {
      const response = await page.request.get(GATEWAY + "/key/info?key=" + key.token_id, {headers});
      expect(response.status()).toBe(200); return (await response.json()).info;
    };
    /** 验证拒绝不转发；无参数/返回，要求明确 401 契约与上游计数不变。 */
    const rejected = async () => {
      const before = await page.request.get(UPSTREAM + "/e2e/request-count"); expect(before.status()).toBe(200);
      const denied = await invoke(); requireAuthorizationError(denied.status(), await denied.json(), 401, kind === "delete" ? undefined : "key_blocked");
      const after = await page.request.get(UPSTREAM + "/e2e/request-count"); expect(after.status()).toBe(200);
      expect(await after.json()).toEqual(await before.json());
    };
    let deleted = false;
    try {
      await page.keyboard.press("Escape");
      if (kind === "unblock") {
        const block = await page.request.post(GATEWAY + "/key/block", {headers, data:{key:key.token_id}});
        expect(block.status(), await block.text()).toBe(200); await rejected();
      } else expect((await invoke()).status()).toBe(200);
      await stableGoto(page, "/api-keys?key=" + key.token_id);
      if (kind === "edit") {
        await page.setViewportSize({ width: 1440, height: 700 });
        const guide = page.getByRole("complementary", { name: t("quotaGuide.title") });
        await expect(guide.locator("details")).not.toHaveAttribute("open");
        await guide.locator("summary").click();
        await expect(guide.getByText(t("quotaGuide.chain"), { exact: true })).toBeVisible();
        await guide.locator("summary").click();
        await expect(page.getByRole("tab", { name: t("Settings"), exact: true })).toHaveCount(0);
        const scroll = page.getByRole("region", { name: t("Key Settings"), exact: true });
        const actions = page.getByRole("group", { name: t("keyEditor.actions"), exact: true });
        // 布局约束由浏览器实测：全宽、可滚动、操作栏与内容区互不遮挡。
        const bounds = await scroll.boundingBox();
        expect(bounds!.width).toBeGreaterThan(1000);
        expect(await scroll.evaluate(node => node.scrollHeight > node.clientHeight)).toBe(true);
        expect(await scroll.evaluate(node => getComputedStyle(node).overflowY)).toBe("scroll");
        const footerBounds = await actions.boundingBox();
        expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(footerBounds!.y + 1);
        expect(Math.abs(bounds!.x - footerBounds!.x)).toBeLessThan(2);
        expect(Math.abs(bounds!.width - footerBounds!.width)).toBeLessThan(2);
        // 窄屏保持同一面板且无横向溢出，保存按钮在视口内。
        await page.setViewportSize({ width: 720, height: 640 });
        expect(await scroll.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
        await expect(actions.getByRole("button", { name: t("Save Changes"), exact: true })).toBeInViewport();
        await page.setViewportSize({ width: 1440, height: 700 });
        await scroll.hover(); await page.mouse.wheel(0, 1800);
        await expect.poll(() => scroll.evaluate(node => node.scrollTop)).toBeGreaterThan(0);
        await expect(page.getByRole("switch", { name: t("keyEditor.enableRotation"), exact: true })).toBeInViewport();
        await expect(actions.getByRole("button", { name: t("Save Changes"), exact: true })).toBeInViewport();
        // 切换真实语言后保留用户输入，覆盖中英文标签。
        await page.getByRole("button", { name: "English", exact: true }).click();
        await expect(page.getByLabel("Key Alias", { exact: true })).toHaveValue(alias);
        await expect(page.getByRole("switch", { name: "Enable Auto-Rotation", exact: true })).toBeVisible();
        await page.getByRole("button", { name: "中文", exact: true }).click();
        await page.getByLabel(t("Max Budget (USD)"), { exact: true }).fill("10");
        await page.getByLabel(t("RPM Limit"), { exact: true }).fill("100");
        await page.getByLabel(t("TPM Limit"), { exact: true }).fill("100000");
        await page.getByLabel(t("keyEditor.expire"), { exact: true }).fill("30d");
        await page.getByRole("textbox", {name:t("Key Alias"), exact:true}).fill(alias + "-renamed");
        const saved = page.waitForResponse(r => new URL(r.url()).pathname === "/key/update" && r.request().method() === "POST");
        await page.getByRole("button", {name:t("Save Changes"), exact:true}).last().click(); expect((await saved).status()).toBe(200);
        const stored = await info();
        expect(stored).toMatchObject({ key_alias: alias + "-renamed", max_budget: 10, rpm_limit: 100, tpm_limit: 100000 });
        expect(stored.expires).toBeTruthy();
        await page.reload(); await expect(page.getByRole("heading").filter({has:page.getByText(alias + "-renamed", {exact:true})})).toBeVisible();
        expect((await invoke()).status()).toBe(200);
      } else {
        const action = kind === "block" ? "Block" : kind === "unblock" ? "Unblock" : "Delete";
        await page.getByRole("button", {name:t("More key actions"), exact:true}).click();
        await page.getByRole("menuitem", {name:t(action + " Key"), exact:true}).click();
        if (kind === "delete") await page.getByPlaceholder(alias, {exact:true}).fill(alias);
        const saved = page.waitForResponse(r => new URL(r.url()).pathname === "/key/" + kind && r.request().method() === "POST");
        await page.getByRole("button", {name:kind === "delete" ? t("common.delete") : t(action), exact:true}).click();
        expect((await saved).status()).toBe(200);
        if (kind === "delete") {
          deleted = true; await expect(page.getByText(alias, {exact:true})).toHaveCount(0);
          expect((await page.request.get(GATEWAY + "/key/info?key=" + key.token_id, {headers})).status()).toBe(404); await rejected();
        } else {
          expect((await info()).blocked).toBe(kind === "block");
          // 当前详情仅在封禁时显示徽标；解封后检查徽标消失及可再次封禁的操作。
          if (kind === "block") await expect(page.getByText(t("Blocked"), {exact:true}).first()).toBeVisible();
          else {
            await expect(page.getByText(t("Blocked"), {exact:true})).toHaveCount(0);
            await page.getByRole("button", {name:t("More key actions"), exact:true}).click();
            await expect(page.getByRole("menuitem", {name:t("Block Key"), exact:true})).toBeVisible();
            await page.keyboard.press("Escape");
          }
          if (kind === "block") await rejected(); else expect((await invoke()).status()).toBe(200);
        }
      }
      await guard.assertOk();
    } finally {
      if (!deleted) expect((await page.request.post(GATEWAY + "/key/delete", {headers, data:{keys:[key.token_id]}})).status()).toBe(200);
      expect((await page.request.get(GATEWAY + "/key/info?key=" + key.token_id, {headers})).status()).toBe(404);
    }
  });
}
/** 注册模型单项生命周期；kind 指定编辑、连接测试、禁用、恢复或删除，title 为报告名称，返回无。
 * 前置独立真实部署，浏览器执行目标操作并回读持久化与数据面；finally 删除部署并核对列表为空。 */
export function registerModelWrite(kind: "edit" | "connection" | "disable" | "enable" | "delete", title: string) {
  test(title, async ({page}) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const headers = {Authorization:"Bearer " + await sessionBearer(page)};
    const name = "e2e-model-" + kind + "-" + Date.now();
    const created = await page.request.post(GATEWAY + "/model/new", {headers, data:{
      model_name:name, model_info:{transport:"bypass_openai_chat", endpoint_types:["chat"]},
      litellm_params:{model:"openai/gpt-4o-mini", custom_llm_provider:"openai", api_key:"sk-fake", api_base:UPSTREAM,
        input_cost_per_token:0.00000015, output_cost_per_token:0.0000006},
    }});
    expect(created.status(), await created.text()).toBe(200);
    const id = (await created.json()).model_info.id;
    /** 读取专属部署；返回真实列表，必须 HTTP200，可证明修改持久化与清理后空列表。 */
    const stored = async () => {
      const response = await page.request.get(GATEWAY + "/v2/model/info?modelId=" + encodeURIComponent(id), {headers});
      expect(response.status()).toBe(200); return (await response.json()).data;
    };
    /** 使用真实管理员会话调用指定模型；publicName 为公开名称，返回响应并产生私有测试账单。 */
    const invoke = (publicName:string) => page.request.post(GATEWAY + "/v1/chat/completions", {headers,
      data:{model:publicName,messages:[{role:"user",content:"Verify independent model " + kind}]}});
    /** 证明不可调用模型未转发；要求明确 400 错误与上游计数不变，异常直接失败。 */
    const denied = async () => {
      const before = await page.request.get(UPSTREAM + "/e2e/request-count"); expect(before.status()).toBe(200);
      const response = await invoke(name); expect(response.status()).toBe(400); expect((await response.json()).error.type).toBeTruthy();
      const after = await page.request.get(UPSTREAM + "/e2e/request-count"); expect(after.status()).toBe(200);
      expect(await after.json()).toEqual(await before.json());
    };
    let deleted = false;
    try {
      if (kind === "enable") {
        expect((await page.request.post(GATEWAY + "/model/disable", {headers,data:{id}})).status()).toBe(200);
        await denied();
      }
      await stableGoto(page, "/models-and-endpoints");
      await page.getByRole("tab", {name:t("pages.models.all"),exact:true}).click();
      await page.getByRole("region", {name:"公开模型 " + name,exact:true}).getByRole("button", {name:"详情",exact:true}).click();
      if (kind === "connection") {
        const before = await stored();
        await page.getByRole("button", {name:"测试连接",exact:true}).click();
        await expect(page.getByRole("status").filter({hasText:"连接正常，模型已响应。"})).toBeVisible();
        expect(await stored()).toEqual(before); expect((await invoke(name)).status()).toBe(200);
      } else if (kind === "edit") {
        await page.getByRole("button", {name:"编辑模型",exact:true}).click();
        await page.getByLabel("对外模型名称 *").fill(name + "-renamed");
        const saved = page.waitForResponse(r => r.request().method() === "PATCH" && new URL(r.url()).pathname.endsWith("/update"));
        await page.getByRole("button", {name:"保存修改",exact:true}).click(); expect((await saved).status()).toBe(200);
        await expect(page.getByRole("heading", {name:name + "-renamed",exact:true})).toBeVisible();
        expect((await stored())[0].model_name).toBe(name + "-renamed"); expect((await invoke(name + "-renamed")).status()).toBe(200);
      } else if (kind === "disable" || kind === "enable") {
        // 页面开关通过部署 PATCH 保存 disabled；等待实际接口，避免成功写入后仍等待旧启停路由。
        const changed = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/update") && r.request().method() === "PATCH");
        await page.getByRole("switch", {name:name + " 启用模型",exact:true}).click(); expect((await changed).status()).toBe(200);
        await expect(page.getByRole("switch", {name:name + " 启用模型",exact:true})).toBeChecked({checked:kind === "enable"});
        expect((await stored())[0].model_info.disabled).toBe(kind === "disable");
        if (kind === "disable") {await expect(page.getByRole("button", {name:"测试连接",exact:true})).toBeDisabled(); await denied();}
        else expect((await invoke(name)).status()).toBe(200);
      } else {
        await page.getByRole("button", {name:"删除",exact:true}).click();
        await page.getByRole("dialog").getByPlaceholder(name).fill(name);
        const removed = page.waitForResponse(r => new URL(r.url()).pathname === "/model/delete" && r.request().method() === "POST");
        await page.getByRole("dialog").getByRole("button", {name:"删除",exact:true}).click(); expect((await removed).status()).toBe(200); deleted = true;
        await expect(page.getByRole("region", {name:"公开模型 " + name,exact:true})).toHaveCount(0); expect(await stored()).toEqual([]); await denied();
      }
      await guard.assertOk();
    } finally {
      if (!deleted) expect((await page.request.post(GATEWAY + "/model/delete", {headers,data:{id}})).status()).toBe(200);
      expect(await stored(), "自建部署应全部删除").toEqual([]);
    }
  });
}
/** 用途：注册独立场景「team member add is listed」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC254() {


test("team member add is listed", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  await page.goto(uiPath("/teams"));
  await page.getByTestId("create-team-button").click();
  await chooseOrganization(page);
  await page.getByTestId("team-name-input").fill("e2e-member-team");
  await page.getByTestId("create-team-submit").click();
  await expect(page.getByText("e2e-member-team").first()).toBeVisible({ timeout: 15_000 });
  await page.getByText("e2e-member-team", { exact: true }).first().click();
  await page.getByRole("tab", { name: t("Members") }).click();
  const member = await page.request.post(`${GATEWAY}/user/new`, {
    headers: { Authorization: `Bearer ${await sessionFrom(page)}`, "Content-Type": "application/json" },
    data: {
      user_id: "e2e-member",
      user_email: "e2e-member@example.com",
      user_role: "user",
      password: "e2e-member-password",
    },
  });
  expect(member.ok(), await member.text()).toBeTruthy();
  await page.getByRole("button", { name: t("Add Member") }).click();
  const memberDialog = page.getByRole("dialog");
  await expect(memberDialog.getByRole("heading", { name: /成员/ })).toBeVisible();
  const emailSearch = memberDialog.getByRole("combobox", { name: t("Email") });
  await emailSearch.click();
  await emailSearch.fill("e2e-member@example.com");
  await page.getByRole("option", { name: "e2e-member@example.com" }).click();
  await memberDialog.getByRole("button", { name: t("Add Member") }).click();
  await expect(page.getByText("e2e-member@example.com").first()).toBeVisible({ timeout: 15_000 });
  await guard.assertOk();
});
}
/** 用途：注册独立场景「router fallback update lists the mapping」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC255() {


test("router fallback update lists the mapping", async ({ page }) => {
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionFrom(page)) };
  const suffix = Date.now().toString();
  const primary = "template-primary-" + suffix,
    backup = "template-backup-" + suffix;
  const templateName = "template-fallback-" + suffix;
  let templateID = "",
    key = "";
  try {
    for (const [name, model] of [
      [primary, "e2e-fallback-500-" + suffix],
      [backup, "custom/template-backup"],
    ]) {
      const result = await page.request.post(GATEWAY + "/model/new", {
        headers,
        data: {
          model_name: name,
          model_info: {
            id: "dep-" + name,
            transport: "bypass_openai_chat",
            endpoint_types: ["chat", "bypass:openai-chat"],
            pricing_source: "manual",
          },
          litellm_params: {
            model,
            custom_llm_provider: "custom",
            api_key: "sk-fake",
            api_base: UPSTREAM,
            input_cost_per_token: 0.000001,
            output_cost_per_token: 0.000002,
          },
        },
      });
      expect(result.status(), await result.text()).toBe(200);
    }
    await stableGoto(page, "/route-templates");
    await page
      .getByRole("button", { name: t("pages.routeTemplates.create"), exact: true })
      .first()
      .click();
    await page.getByLabel(t("pages.routeTemplates.name"), { exact: true }).fill(templateName);
    await page.getByRole("tab", { name: "故障转移", exact: true }).click();
    await page.getByRole("button", { name: "添加故障转移", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "配置模板故障转移", exact: true });
    await dialog.getByLabel("故障转移主模型", { exact: true }).selectOption(primary);
    await dialog.getByRole("button", { name: "添加回退目标", exact: true }).click();
    await dialog.getByLabel("回退目标 1", { exact: true }).selectOption(backup);
    await dialog.getByRole("button", { name: "保存到模板", exact: true }).click();
    const saving = page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/route_template/new" && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "保存模板", exact: true }).click();
    const saved = await saving;
    expect(saved.status(), await saved.text()).toBe(200);
    templateID = (await saved.json()).id;
    expect(templateID).toBeTruthy();
    const stored = await page.request.get(GATEWAY + "/route_template/" + templateID, { headers });
    expect(stored.status()).toBe(200);
    expect((await stored.json()).body.fallbacks).toEqual([{ [primary]: [backup] }]);
    await page.reload();
    const row = page.getByRole("row").filter({ has: page.getByText(templateName, { exact: true }) });
    await row.getByRole("button", { name: t("pages.routeTemplates.edit"), exact: true }).click();
    await page.getByRole("tab", { name: "故障转移", exact: true }).click();
    await expect(page.getByRole("region", { name: "模板故障转移", exact: true })).toContainText("1. " + backup);
    const teams = await (await page.request.get(GATEWAY + "/v2/team/list?page=1&page_size=500", { headers })).json();
    const teamID = teams.teams.find((row: any) => row.team_alias === "e2e-fixture-team").team_id;
    const jwt = (await page.context().cookies()).find((c) => c.name === "token")!.value;
    const userID = JSON.parse(Buffer.from(jwt.split(".")[1], "base64url").toString()).user_id;
    const minted = await page.request.post(GATEWAY + "/key/generate", {
      headers,
      data: {
        key_alias: templateName,
        key_type: "llm_api",
        team_id: teamID,
        user_id: userID,
        route_template_id: templateID,
      },
    });
    expect(minted.status(), await minted.text()).toBe(200);
    key = (await minted.json()).key;
    const response = await page.request.post(GATEWAY + "/bypass/openai/v1/chat/completions", {
      headers: { Authorization: "Bearer " + key },
      data: { model: primary, messages: [{ role: "user", content: "template fallback" }] },
    });
    expect(response.status(), await response.text()).toBe(200);
    expect((await response.json()).model).toBe("custom/template-backup");
    const callID = response.headers()["x-litellm-call-id"];
    await expect
      .poll(async () => {
        const log = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
        return log.ok() ? Number((await log.json()).spend) : 0;
      })
      .toBeGreaterThan(0);
  } finally {
    if (key) {
      expect(
        (await page.request.post(GATEWAY + "/key/update", { headers, data: { key, route_template_id: "" } })).status(),
      ).toBe(200);
      expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [key] } })).status()).toBe(200);
    }
    if (templateID)
      expect(
        (await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers })).status(),
      ).toBe(200);
    for (const name of [primary, backup]) {
      const deleted = await page.request.post(GATEWAY + "/model/delete", { headers, data: { id: "dep-" + name } });
      expect([200, 404]).toContain(deleted.status());
    }
  }
});
}
/** 用途：注册独立场景「admin panel saves prompt storage and hides the unused settings」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC256() {


test("admin panel saves prompt storage and hides the unused settings", async ({ page }) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  await page.goto(uiPath("/admin-panel"));
  await page.getByRole("tab", { name: "日志设置", exact: true }).click();
  // 标签页与卡片共享标题；限定已激活面板，防止重复文案导致严格定位失败。
  await expect(page.getByRole("tabpanel", { name: t("Logging Settings"), exact: true })).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("tab", { name: "单点登录设置", exact:true })).toBeVisible();
  await expect(page.getByRole("tab", { name: t("Security Settings") })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: "SCIM", exact:true })).toBeVisible();
  await expect(page.getByRole("tab", { name: t("Hashicorp Vault") })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: t("CyberArk Conjur") })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: t("Plugins") })).toHaveCount(0);
  const promptSwitch = page.getByRole("switch", {name:t("Store Prompts in Spend Logs"), exact:true});
  const initial = await promptSwitch.isChecked();
  try {
    await promptSwitch.setChecked(!initial);
    await page.getByRole("button", { name: t("Save Settings") }).click();
    await expect(page.getByText(t("Spend logs settings updated successfully"))).toBeVisible({ timeout: 15_000 });
    await page.reload();
    await page.getByRole("tab", {name:"日志设置",exact:true}).click();
    await expect(promptSwitch).toHaveAttribute("aria-checked", String(!initial));
  } finally {
    // 恢复当前私有 schema 的初值，避免改变后续归档的脱敏前置条件。
    await promptSwitch.setChecked(initial);
    await page.getByRole("button", {name:t("Save Settings")}).click();
    await expect(page.getByText(t("Spend logs settings updated successfully"))).toBeVisible();
  }
  await guard.assertOk();
});
}

import { expect, test, type Page } from "@playwright/test";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";

/** 注册单层预算耗尽闭环；参数为层级、固定标题，无返回值。
 * 每条创建独立资源，创建零预算验证拒绝，再通过页面提高额度恢复；验证错误归因、未转发及未扣费，finally 回收数据。
 */
export function registerBudgetBoundary(layer: "organization" | "team" | "personal" | "project" | "key", title: string) {
  test(title, async ({ page }) => {
    test.setTimeout(120_000);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const suffix = layer + "-budget-" + Date.now();
    let organization = "", team = "", user = "", project = "", keyID = "", secret = "";
    /** 创建真实资源；path 为路由、data 为正文，返回 JSON；非 200 直接失败。 */
    const create = async (path: string, data: Record<string, unknown>) => {
      const response = await page.request.post(GATEWAY + path, { headers, data });
      expect(response.status(), await response.text()).toBe(200);
      return response.json();
    };
    /** 读取当前层持久化快照，返回额度及支出；只读，拒绝前后比较用于防止误扣费。 */
    const info = async () => {
      const path = layer === "organization" ? "/organization/info?organization_id=" + organization
        : layer === "team" ? "/team/info?team_id=" + team
        : layer === "project" ? "/project/info?project_id=" + project
        : layer === "personal" ? "/user/info?user_id=" + user : "/key/info?key=" + keyID;
      const response = await page.request.get(GATEWAY + path, { headers });
      expect(response.status(), await response.text()).toBe(200);
      const data = await response.json();
      return layer === "organization" ? data : layer === "team" ? data.team_info : layer === "project" ? data.project : layer === "personal" ? data.user_info : data.info;
    };
    /** 页面保存预算；value 为新限额，无返回；验证保存契约与持久化，副作用只限自建资源。 */
    const configure = async (value: number) => {
      const path = layer === "organization" ? "/organizations?org=" + organization
        : layer === "team" ? "/teams?team=" + team
        : layer === "project" ? "/projects?project=" + project
        : layer === "personal" ? "/users?user=" + user : "/api-keys?key=" + keyID;
      await stableGoto(page, path);
      if (layer === "personal") {
        await page.getByRole("button", { name: t("pages.users.editUser"), exact: true }).click();
      } else if (layer === "project") {
        await page.getByRole("button", { name: t("Edit Project"), exact: true }).click();
      } else {
        await page.getByRole("tab", { name: t("Settings"), exact: true }).click();
        await page.getByRole("button", { name: t("Edit Settings"), exact: true }).click();
      }
      // 用户预算标签包含“无限额”复选框文本，按语义标签前缀定位数字框。
      const budgetInput = layer === "personal"
        ? page.getByRole("spinbutton", { name: new RegExp("^" + t("pages.users.maxBudget")) })
        : page.getByLabel(t("Max Budget (USD)"), { exact: true });
      await budgetInput.fill(String(value));
      const saving = page.waitForResponse(r => new URL(r.url()).pathname === "/" + (layer === "personal" ? "user" : layer) + "/update" && r.request().method() === (layer === "organization" ? "PATCH" : "POST"));
      await page.getByRole("button", { name: t(layer === "personal" ? "pages.users.saveChanges" : "Save Changes"), exact: true }).click();
      const saved = await saving;
      expect(saved.status(), await saved.text()).toBe(200);
      expect((await info())["max_budget"]).toBe(value);
    };
    /** 调用真实推理；phase 隔离缓存键，返回响应；只有恢复请求允许调用供应商。 */
    const invoke = (phase: string) => page.request.post(GATEWAY + "/v1/chat/completions", {
      headers: { Authorization: "Bearer " + secret },
      data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Verify " + suffix + " " + phase }] },
    });
    try {
      organization = (await create("/organization/new", { organization_alias: suffix, max_budget: layer === "organization" ? 0 : 1000 })).organization_id;
      team = (await create("/team/new", { team_alias: suffix, organization_id: organization, max_budget: layer === "team" ? 0 : null })).team_id;
      user = (await create("/user/new", { user_email: suffix + "@example.com", password: "limit-password", ...(layer === "personal" ? {} : { team_id: team, team_role: "user" }), max_budget: layer === "personal" ? 0 : null })).user_id;
      if (layer === "project") project = (await create("/project/new", { project_alias: suffix, team_id: team, max_budget: 0 })).project_id;
      // 密钥页面默认个人范围；密钥层使用登录管理员自己的密钥，其他层使用成员密钥验证继承链。
      const key = await create("/key/generate", { ...(layer === "key" ? {} : { user_id: user }), key_alias: suffix, ...(project ? { project_id: project } : {}), ...(layer === "key" ? { max_budget: 0 } : {}) });
      keyID = key.token_id; secret = key.key;
      // 初始零预算通过真实创建持久化，恢复再通过页面编辑。
      const before = await info();
      const upstream = await requestCount(page);
      const blocked = await invoke("blocked");
      expect(blocked.status(), await blocked.text()).toBe(403);
      // 预算协议明确使用英文范围名；明确校验归因，防止其他层拒绝误算通过。
      const scope = { organization: "Organization", team: "Team", personal: "User", project: "Project", key: "Key" }[layer];
      expect(await blocked.json()).toMatchObject({ error: { type: "budget_exceeded", code: "403", message: scope + " budget has been exceeded" } });
      expect(await requestCount(page), "预算拒绝不得转发上游").toEqual(upstream);
      const after = await info();
      expect(after.spend, "预算拒绝不得增加支出").toEqual(before.spend);
      expect(after.max_budget).toEqual(before.max_budget);
      expect(after["max_budget"]).toBe(0);
      await configure(10);
      const recovered = await invoke("recovered");
      expect(recovered.status(), await recovered.text()).toBe(200);
      expect((await recovered.json()).choices[0].message.content).toBe("e2e-ok");
      expect(await requestCount(page), "恢复后必须真实调用供应商").not.toEqual(upstream);
      const callID = recovered.headers()["x-litellm-call-id"];
      expect(callID).toBeTruthy();
      await expect.poll(async () => (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).status()).toBe(200);
      await stableGoto(page, "/logs");
      await expect(page.getByRole("main")).toContainText("gpt-4o-mini");
    } finally {
      test.setTimeout(test.info().timeout + 30_000);
      if (keyID) {
        expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).status()).toBe(200);
        expect((await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).status()).toBe(404);
      }
      if (project) expect((await page.request.post(GATEWAY + "/project/delete", { headers, data: { project_id: project } })).status()).toBe(200);
      if (user) expect((await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: user } })).status()).toBe(200);
      if (team) expect((await page.request.post(GATEWAY + "/team/delete", { headers, data: { team_id: team } })).status()).toBe(200);
      if (organization) expect((await page.request.delete(GATEWAY + "/organization/delete", { headers, data: { organization_id: organization } })).status()).toBe(200);
    }
  });
}

/** 读取本地上游真实计数；page 为当前上下文，返回只读 JSON；接口异常直接失败。 */
async function requestCount(page: Page) {
  const response = await page.request.get(UPSTREAM + "/e2e/request-count");
  expect(response.status()).toBe(200);
  return response.json();
}

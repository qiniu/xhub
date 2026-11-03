import { expect, test } from "@playwright/test";
import { GATEWAY, UPSTREAM, login, loginAdmin, sessionBearer, t, watchGateway } from "./helpers";
import { requireAuthorizationError } from "./assertions";

/** 注册成员单项越权；kind 指定模型/组织/用户写入，title 为独立报告名称，返回无。
 * 前置管理员自建成员并通过浏览器登录；验证导航、403 契约、对应资源快照及无上游调用；finally 删除成员并回读 404。 */
export function registerMemberWrite(kind: "model" | "organization" | "user", title: string) {
  test(title, async ({ page }) => {
    const guard = watchGateway(page);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const suffix = Date.now() + "-" + kind;
    const email = "member-permissions-" + suffix + "@example.com";
    const created = await page.request.post(GATEWAY + "/user/new", {
      headers, data: { user_email: email, password: "member-pass", user_role: "user" },
    });
    expect(created.status(), await created.text()).toBe(200);
    const user = await created.json();
    try {
      await login(page, email, "member-pass");
      await expect(page.getByText(t("nav.apiKeys")).first()).toBeVisible();
      await expect(page.locator('a[href="/ui/admin-panel"]')).toHaveCount(0);
      await expect(page.locator('a[href="/ui/models-and-endpoints"]')).toHaveCount(0);
      const memberHeaders = { Authorization: "Bearer " + await sessionBearer(page) };
      const spec = {
        model: { list: "/v2/model/info", path: "/model/new", data: { model_name: "forbidden-" + suffix, litellm_params: { model: "openai/gpt-4o-mini" } } },
        organization: { list: "/organization/list", path: "/organization/new", data: { organization_alias: "forbidden-" + suffix } },
        user: { list: "/user/list", path: "/user/new", data: { user_email: "forbidden-" + suffix + "@example.com" } },
      }[kind];
      /** 读取管理员资源快照；返回真实 JSON，读取错误使测试失败，仅用于拒绝前后比较。 */
      const snapshot = async () => {
        const response = await page.request.get(GATEWAY + spec.list, { headers });
        expect(response.status(), await response.text()).toBe(200); return response.json();
      };
      /** 读取上游计数；返回快照，控制端失败不计拒绝通过，无业务副作用。 */
      const count = async () => {
        const response = await page.request.get(UPSTREAM + "/e2e/request-count");
        expect(response.status()).toBe(200); return response.json();
      };
      const before = await snapshot(), upstream = await count();
      const denied = await page.request.post(GATEWAY + spec.path, { headers: memberHeaders, data: spec.data });
      requireAuthorizationError(denied.status(), await denied.json(), 403);
      expect(await snapshot(), kind + " 越权不能改变持久化数据").toEqual(before);
      expect(await count(), "越权不能调用上游").toEqual(upstream);
      await guard.assertOk();
    } finally {
      const deleted = await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: user.user_id } });
      expect(deleted.status(), await deleted.text()).toBe(200);
      expect((await page.request.get(GATEWAY + "/user/info?user_id=" + user.user_id, { headers })).status()).toBe(404);
    }
  });
}

import { expect, test as base } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate, type Locale } from "../src/i18n/translate";
import { matchesGatewayResponse } from "./gateway-response.cjs";

type Resource = { name: string; headers: Record<string, string>; customers?: boolean };
/** 独立请求上下文清理固定唯一标签；即使创建响应丢失、页面关闭或业务超时仍按已知name删除。
 * Playwright 注入上下文与runFixture，返回仅本例资源；删除HTTP错误或详情仍存在均令测试失败，最后释放上下文。
 */
export const test = base.extend<{ tagResource: Resource }>({
  tagResource: [
    async ({ playwright }, runFixture, info) => {
      const resource: Resource = { name: `e2e-tag-${info.testId}`, headers: {} };
      const api = await playwright.request.newContext({ timeout: 10000 });
      try {
        await runFixture(resource);
      } finally {
        try {
          if (resource.headers.Authorization) {
            const results = await Promise.allSettled(
              [resource.name, resource.name + "-collision"].map(async (name) => {
                const deleted = await api.post(GATEWAY + "/tag/delete", { headers: resource.headers, data: { name } });
                expect(deleted.status(), "标签清理HTTP失败").toBe(200);
                expect([0, 1], "固定资源删除计数必须合法").toContain((await deleted.json()).deleted);
                const absent = await api.post(GATEWAY + "/tag/info", { headers: resource.headers, data: { name } });
                expect(absent.status(), "清理后标签仍存在").toBe(404);
              }),
            );
            if (resource.customers) {
              const customers = await Promise.allSettled(
                ["customer", "end_user"].map(async (kind) => {
                  const deleted = await api.post(GATEWAY + "/" + kind + "/delete", {
                    headers: resource.headers,
                    data: { user_id: resource.name },
                  });
                  expect(deleted.status(), "客户清理HTTP失败").toBe(200);
                  expect([0, 1], "客户删除计数必须合法").toContain((await deleted.json()).deleted);
                }),
              );
              results.push(...customers);
              const listed = await api.get(GATEWAY + "/customer/list", { headers: resource.headers });
              expect(listed.status(), "客户清理后读取失败").toBe(200);
              expect(
                (await listed.json()).data.filter((row: { user_id: string }) => row.user_id === resource.name),
                "客户清理后有残留",
              ).toEqual([]);
            }
            expect(
              results.filter((r) => r.status === "rejected"),
              "所有标签清理必须成功",
            ).toEqual([]);
          }
        } finally {
          await api.dispose();
        }
      }
    },
    { timeout: 30000 },
  ],
});

/** 标签真实读写流程：locale决定EN/中文，includeCustomers开启客户CRUD兼容验证；HTTP建改标签，调试台显示/选择/刷新并发送真实聊天，最后删除。
 * 不恢复标签管理页，不将配置额度当准入规则；英文示例与原始中文描述保留，独立fixture清理所有结果路径。
 */
export function registerTagCatalog(locale: Locale, includeCustomers = false) {
  test(
    (includeCustomers ? "metadata configuration lifecycle " : "tag catalog read after write ") + locale,
    async ({ page, tagResource: r }) => {
      const tr = (key: string) => translate(locale, key);
      await loginAdmin(page);
      await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
      r.headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
      r.customers = includeCustomers;
      if (includeCustomers) {
        for (const kind of ["customer", "end_user"]) {
          const createdCustomer = await page.request.post(GATEWAY + "/" + kind + "/new", {
            headers: r.headers,
            data: { user_id: r.name, description: "原始客户说明", max_budget: 500, blocked: false },
          });
          expect(createdCustomer.status()).toBe(200);
          expect((await createdCustomer.json()).description).toBe("原始客户说明");
          const updated = await page.request.post(GATEWAY + "/" + kind + "/update", {
            headers: r.headers,
            data: { user_id: r.name, max_budget: 0, blocked: true },
          });
          expect(updated.status()).toBe(200);
          const invalid = await page.request.post(GATEWAY + "/" + kind + "/update", {
            headers: { ...r.headers, "Content-Type": "application/json" },
            data: "[]",
          });
          expect(invalid.status()).toBe(400);
        }
        for (const kind of ["customer", "end_user"]) {
          const listed = await page.request.get(GATEWAY + "/" + kind + "/list", { headers: r.headers });
          expect(listed.status()).toBe(200);
          const rows = (await listed.json()).data.filter((row: { user_id: string }) => row.user_id === r.name);
          expect(rows).toHaveLength(2);
          expect(
            rows.every((row: { max_budget: number; blocked: boolean }) => row.max_budget === 0 && row.blocked),
          ).toBe(true);
        }
      }
      const created = await page.request.post(GATEWAY + "/tag/new", {
        headers: r.headers,
        data: { name: r.name, description: "原始用户说明", models: [], max_budget: 0 },
      });
      expect(created.status(), await created.text()).toBe(200);
      await stableGoto(page, "/playground");
      await page.getByPlaceholder(tr("Select a Model"), { exact: true }).click();
      await page.getByRole("option", { name: /gpt-4o-mini/ }).click();
      await page
        .locator("summary")
        .filter({ hasText: new RegExp("^" + tr("Tags") + "$") })
        .click();
      await page.getByRole("combobox", { name: tr("Select or create tags"), exact: true }).click();
      await expect(page.getByRole("option", { name: r.name + " 原始用户说明", exact: true })).toBeVisible();
      await page.getByRole("option", { name: r.name + " 原始用户说明", exact: true }).click();
      await page.keyboard.press("Escape");
      const edited = await page.request.post(GATEWAY + "/tag/update", {
        headers: r.headers,
        data: { name: r.name, description: "Updated description" },
      });
      expect(edited.status()).toBe(200);
      await page.reload();
      await page
        .locator("summary")
        .filter({ hasText: new RegExp("^" + tr("Tags") + "$") })
        .click();
      await page.getByRole("combobox", { name: tr("Select or create tags"), exact: true }).click();
      await expect(page.getByRole("option", { name: r.name + " Updated description", exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
      // 用真实写接口制造两个同名展示记录，验证列表500不会在页面伪装成空配置；不拦截后台响应。
      const collision = await page.request.post(GATEWAY + "/tag/new", {
        headers: r.headers,
        data: { name: r.name + "-collision", tag_name: r.name },
      });
      expect(collision.status()).toBe(200);
      const failedList = page.waitForResponse((res) =>
        matchesGatewayResponse(res.url(), res.request().method(), "/tag/list", "GET"),
      );
      await page.reload();
      expect((await failedList).status()).toBe(500);
      await page
        .locator("summary")
        .filter({ hasText: new RegExp("^" + tr("Tags") + "$") })
        .click();
      await expect(page.getByRole("alert").filter({ hasText: tr("tagSelector.loadFailed") })).toBeVisible();
      expect(
        (
          await page.request.post(GATEWAY + "/tag/delete", {
            headers: r.headers,
            data: { name: r.name + "-collision" },
          })
        ).status(),
      ).toBe(200);
      await page.reload();
      await page
        .locator("summary")
        .filter({ hasText: new RegExp("^" + tr("Tags") + "$") })
        .click();
      await expect(page.getByRole("alert").filter({ hasText: tr("tagSelector.loadFailed") })).toHaveCount(0);
      const sent = page.waitForResponse((res) =>
        matchesGatewayResponse(res.url(), res.request().method(), "/chat/completions", "POST"),
      );
      await page.getByTestId("chat-composer-input").fill("Reply with a short greeting.");
      await page.getByTestId("chat-send-button").click();
      const chat = await sent;
      expect(chat.status()).toBe(200);
      expect(chat.request().headers()["x-litellm-tags"]).toBe(r.name);
      await expect(page.getByTestId("message-surface").filter({ hasText: "e2e-ok" })).toBeVisible();
      const deleted = await page.request.post(GATEWAY + "/tag/delete", { headers: r.headers, data: { name: r.name } });
      expect(deleted.status()).toBe(200);
      expect((await deleted.json()).deleted).toBe(1);
      await page.reload();
      await page
        .locator("summary")
        .filter({ hasText: new RegExp("^" + tr("Tags") + "$") })
        .click();
      await page.getByRole("combobox", { name: tr("Select or create tags"), exact: true }).click();
      await expect(page.getByRole("option").filter({ hasText: r.name })).toHaveCount(0);
    },
  );
}

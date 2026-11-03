import { test, expect } from "@playwright/test";
import { once } from "node:events";
import { randomUUID } from "node:crypto";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { GATEWAY, loginAdmin, sessionBearer } from "./helpers";

/** 注册独立 Swagger 中英真实调用；前置站点构建、真实网关和隔离PG。
 * 浏览器创建用户→读回，并验证未授权401、错误显示及授权清除；finally 删除用户并关闭静态服务，无生产依赖。 */
export function registerSwaggerReference(lang: "zh" | "en") {
  test("Swagger reference real user lifecycle " + lang, async ({ page }) => {
    // Playwright 的 CommonJS 转换不能加载含 import.meta 的站点 ESM；
    // 用原生 import 加载固定本地路径，不接受页面或用户传入的脚本。
    const nativeImport = new Function("url", "return import(url)") as (
      url: string,
    ) => Promise<{
      serve: (options: { directory: string; port: number; mount: string }) => import("node:http").Server;
    }>;
    const { serve } = await nativeImport(pathToFileURL(resolve(process.cwd(), "../website/src/serve.mjs")).href);
    page.setDefaultTimeout(15000);
    const server = serve({ directory: resolve(process.cwd(), "../website/dist"), port: 0, mount: "/xhub/" });
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("Missing static server port");
    const base = `http://127.0.0.1:${address.port}/xhub/reference/${lang}/index.html?domain=users`;
    const labels =
      lang === "zh"
        ? { gateway: "网关地址", connect: "启用接口调用", domain: "接口领域", readOnly: "只读模式：尚未设置调用目标。" }
        : {
            gateway: "Gateway URL",
            connect: "Enable API requests",
            domain: "API domain",
            readOnly: "Read-only: no request target configured.",
          };
    const email = "swagger-" + randomUUID() + "@example.test";
    let bearer = "",
      userID = "";
    try {
      await loginAdmin(page);
      bearer = await sessionBearer(page);
      await page.goto(base);
      await expect(page.getByRole("status")).toHaveText(labels.readOnly);
      await page.getByLabel(labels.gateway, { exact: true }).fill("javascript:alert(1)");
      await page.getByRole("button", { name: labels.connect, exact: true }).click();
      await expect(page.getByRole("alert")).toBeVisible();
      await page.getByLabel(labels.gateway, { exact: true }).fill(GATEWAY);
      await page.getByRole("button", { name: labels.connect, exact: true }).click();
      await expect(page.getByRole("status")).toContainText(GATEWAY);
      await page.getByRole("button", { name: /^POST \/user\/new / }).click();
      await page.getByRole("button", { name: "Try it out", exact: true }).click();
      const body = {
        user_email: email,
        password: "Swagger-Test-123",
        user_alias: "原始输入 Keep unchanged",
        max_budget: 10,
      };
      await page.getByRole("textbox", { name: "", exact: true }).fill(JSON.stringify(body));
      /** 点击当前操作，观察真实网关响应；路径与状态由调用方指定，局部等待明确失败位置，不替换接口。 */
      const execute = async (path: string, status: number) => {
        const [response] = await Promise.all([
          page.waitForResponse((r) => r.url().startsWith(GATEWAY + path) && r.request().method() !== "OPTIONS", {
            timeout: 15000,
          }),
          page.getByRole("button", { name: "Execute", exact: true }).click(),
        ]);
        expect(response.status(), await response.text()).toBe(status);
        return response.json();
      };
      const rejected = await execute("/user/new", 401);
      expect(rejected.error).toBeTruthy();
      await expect(page.getByText("Error: Unauthorized", { exact: true })).toBeVisible();
      await page.getByRole("button", { name: "Authorize", exact: true }).click();
      await page.getByRole("textbox", { name: "auth-bearer-value", exact: true }).fill(bearer);
      await page.getByRole("button", { name: "Apply credentials", exact: true }).click();
      await page.getByRole("button", { name: "Close", exact: true }).click();
      const created = await execute("/user/new", 200);
      userID = created.user_id;
      expect(userID).toBeTruthy();
      expect(created.user_alias).toBe(body.user_alias);
      // 浏览器显示实际响应，同时独立读回验证数据库持久化，不以静态示例冒充创建结果。
      await expect(page.getByText(email, { exact: false }).last()).toBeVisible();
      const stored = await page.request.get(GATEWAY + "/user/info?user_id=" + userID, {
        headers: { Authorization: "Bearer " + bearer },
      });
      expect(stored.status()).toBe(200);
      expect((await stored.json()).user_info.user_alias).toBe(body.user_alias);
      await page.getByRole("button", { name: /^POST \/user\/new / }).click();
      await page.getByRole("button", { name: /^GET \/user\/info / }).click();
      await page.getByRole("button", { name: "Try it out", exact: true }).click();
      await page.getByRole("textbox", { name: "user_id", exact: true }).fill(userID);
      expect((await execute("/user/info", 200)).user_info.user_email).toBe(email);
      // 领域切换清除 Swagger 授权；目标仍由用户显式设置，不能把旧凭据带到新实例。
      await page.getByLabel(labels.domain, { exact: true }).selectOption("usage");
      await expect(page.getByRole("status")).toContainText(GATEWAY);
      await page.getByLabel(labels.domain, { exact: true }).selectOption("users");
      await page.getByRole("button", { name: /^GET \/user\/info / }).click();
      await page.getByRole("button", { name: "Try it out", exact: true }).click();
      await page.getByRole("textbox", { name: "user_id", exact: true }).fill(userID);
      expect((await execute("/user/info", 401)).error).toBeTruthy();
      await page.reload();
      await expect(page.getByRole("status")).toHaveText(labels.readOnly);
      await expect(page.getByLabel(labels.gateway, { exact: true })).toHaveValue("");
      expect(
        await page.evaluate((token) => JSON.stringify({ ...localStorage, ...sessionStorage }).includes(token), bearer),
      ).toBe(false);
    } finally {
      try {
        if (userID) {
          const deleted = await page.request.post(GATEWAY + "/user/delete", {
            headers: { Authorization: "Bearer " + bearer },
            data: { user_id: userID },
          });
          expect(deleted.status(), await deleted.text()).toBe(200);
        }
      } finally {
        await new Promise<void>((done, reject) => server.close((err) => (err ? reject(err) : done())));
      }
    }
  });
}

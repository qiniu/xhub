import { test, expect } from "@playwright/test";
import fs from "node:fs/promises";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

// 双语完整导入流程：私有数据库、真实下载/解析/创建/回读；验证重复导入不新增、错误行不提交。
// 所有用户在finally删除，整个私有schema由运行器回收；无需供应商凭据或真实员工信息。
export function registerUserCsvScenario(locale: "en" | "zh-CN") {
  test(`CSV user template and real import ${locale}`, async ({ page }) => {
    await loginAdmin(page);
    await page
      .getByRole("button", { name: locale === "en" ? "English" : translate("en", "language.zhCN"), exact: true })
      .click();
    const tr = (key: string, vars?: Record<string, number>) => translate(locale, `userCsv.${key}`, vars);
    const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
    const email = `csv-${locale}-${Date.now()}@example.invalid`;
    let id = "";
    try {
      await stableGoto(page, "/users");
      await page.getByRole("button", { name: tr("title"), exact: true }).click();
      const dialog = page.getByRole("dialog");
      const downloadPromise = page.waitForEvent("download");
      await dialog.getByRole("button", { name: tr("template"), exact: true }).click();
      const downloaded = await downloadPromise;
      expect(downloaded.suggestedFilename()).toBe("users-template.csv");
      const template = await fs.readFile((await downloaded.path())!, "utf8");
      expect(template).toContain("user_email,user_role,password,user_alias,team_id,max_budget");
      expect(template).not.toMatch(/[\u4e00-\u9fff]/);
      const csv = template.replace("new.user@example.com", email).replace("New User", "测试名字");
      await dialog
        .getByLabel(tr("upload"))
        .setInputFiles({ name: "users.csv", mimeType: "text/csv", buffer: Buffer.from(csv.replace(/10$/, "10abc")) });
      await expect(dialog.getByText(tr("budgetError"))).toBeVisible();
      await expect(dialog.getByRole("button", { name: tr("create", { count: 1 }) })).toBeDisabled();
      await dialog
        .getByLabel(tr("upload"))
        .setInputFiles({ name: "users.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
      await expect(dialog.getByRole("button", { name: tr("create", { count: 1 }) })).toBeEnabled();
      const createdPromise = page.waitForResponse(
        (r) => r.url().endsWith("/user/new") && r.request().method() === "POST",
      );
      await dialog.getByRole("button", { name: tr("create", { count: 1 }) }).click();
      const created = await createdPromise;
      expect(created.status()).toBe(200);
      id = (await created.json()).user_id;
      await expect(dialog.getByRole("status")).toHaveText(tr("summary", { success: 1, failed: 0 }));
      const read = await page.request.get(GATEWAY + "/user/info?user_id=" + id, { headers });
      expect(read.status()).toBe(200);
      const user = (await read.json()).user_info;
      expect(user.max_budget).toBe(10);
      expect(user.user_alias).toBe("测试名字");
      const signedIn = await page.request.post(GATEWAY + "/v2/login", {
        data: { username: email, password: "Replace-before-import-123!" },
      });
      expect(signedIn.status()).toBe(200);
      const resultPromise = page.waitForEvent("download");
      await dialog.getByRole("button", { name: tr("results") }).click();
      const results = await fs.readFile((await (await resultPromise).path())!, "utf8");
      expect(results).toContain(id);
      expect(results).not.toContain("Replace-before-import");
      await dialog.getByRole("button", { name: tr("reset") }).click();
      await expect(dialog.getByText(email)).toHaveCount(0);
      await dialog
        .getByLabel(tr("upload"))
        .setInputFiles({ name: "users.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
      const duplicateResponse = page.waitForResponse(r => r.url().endsWith("/user/new") && r.request().method() === "POST");
      await dialog.getByRole("button", { name: tr("create", { count: 1 }) }).click();
      expect((await duplicateResponse).status()).toBe(409);
      await expect(dialog.getByRole("status")).toHaveText(tr("summary", { success: 0, failed: 1 }));
      await expect(dialog.getByText(tr("requestError"))).toBeVisible();
    } finally {
      if (id) {
        const deleted = await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: id } });
        expect(deleted.status(), await deleted.text()).toBe(200);
      }
    }
  });
}

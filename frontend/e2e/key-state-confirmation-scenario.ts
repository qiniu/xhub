import { expect, test } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";
import { translate } from "../src/i18n/translate";

// 按locale注册异常成功响应测试，返回无：覆盖未送后台及后台已写入但响应损坏，再恢复真实操作；供双语独立用例调用。
// 前置隔离PG/网关/受控模型；只在浏览器边界注入异常回复，详情回读和推理均请求真实后台；finally删除测试Key。
export function registerKeyStateConfirmation(locale: "en" | "zh-CN") {
  test("key state requires explicit confirmation " + locale, async ({ page }) => {
    test.setTimeout(120000);
    const tr = (key: string) => translate(locale, key);
    await loginAdmin(page);
    await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const alias = "Confirmation 用户 " + locale;
    const created = await page.request.post(GATEWAY + "/key/generate", { headers, data: { key_alias: alias } });
    expect(created.status(), await created.text()).toBe(200);
    const { token_id: id, key: token } = await created.json();
    try {
      /** 真实详情与模型准入核验；blocked为期望持久状态，返回Promise，失败定位接口，不以UI/替身断言替代后台。 */
      const verifyBackend = async (blocked: boolean) => {
        const detail = await page.request.get(GATEWAY + "/key/info?key=" + id, { headers });
        expect(detail.status(), await detail.text()).toBe(200);
        expect((await detail.json()).info).toMatchObject({ blocked, key_alias: alias });
        const chat = await page.request.post(GATEWAY + "/v1/chat/completions", {
          headers: { Authorization: "Bearer " + token },
          data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Verify key confirmation behavior." }] },
        });
        expect(chat.status(), await chat.text()).toBe(blocked ? 401 : 200);
        if (!blocked) expect((await chat.json()).choices[0].message.content).toContain("e2e-ok");
      };
      /** 打开真实页面的状态确认框；blocked为目标，仅导航和交互，不提交写入，供故障与恢复用例共用。 */
      const openAction = async (blocked: boolean) => {
        await page.getByRole("button", { name: tr("More key actions"), exact: true }).click();
        await page.getByRole("menuitem", { name: tr(blocked ? "Block Key" : "Unblock Key"), exact: true }).click();
      };
      await stableGoto(page, "/api-keys?key=" + id);
      await expect(page.getByRole("heading", { name: alias, exact: true })).toBeVisible();
      await verifyBackend(false);
      for (const blocked of [true, false]) {
        const path = blocked ? "/key/block" : "/key/unblock";
        for (const forwarded of [false, true]) {
          await openAction(blocked);
          // 仅篡改测试浏览器收到的响应；forwarded=true时后台真实执行，验证不能把未知结果说成“未写入”。
          await page.route("**" + path, async route => {
            if (forwarded) {
              const actual = await route.fetch();
              expect(actual.status(), await actual.text()).toBe(200);
              expect(await actual.json()).toEqual({ blocked });
              await route.fulfill({ response: actual, body: blocked ? "" : "null" });
            } else {
              await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(blocked ? {} : { blocked: true }) });
            }
          }, { times: 1 });
          const response = page.waitForResponse(r => r.url().endsWith(path) && r.request().method() === "POST");
          await page.getByRole("dialog").getByRole("button", { name: tr(blocked ? "Block" : "Unblock"), exact: true }).click();
          expect((await response).status()).toBe(200);
          await expect(page.getByText(tr("keyActions.invalidStateResponse"), { exact: true })).toBeVisible();
          await expect(page.getByText(tr(blocked ? "Key blocked" : "Key unblocked"), { exact: true })).toHaveCount(0);
          await expect(page.getByRole("dialog")).toBeVisible();
          await verifyBackend(forwarded ? blocked : !blocked);
          await page.getByRole("dialog").getByRole("button", { name: tr("Cancel"), exact: true }).click();
          await page.reload();
          await expect(page.getByRole("heading", { name: alias, exact: true })).toBeVisible();
        }
      }
      // 故障注入结束后沿真实接口完成禁用和恢复，必须看到正确通知与实际准入变化。
      for (const blocked of [true, false]) {
        await openAction(blocked);
        const response = page.waitForResponse(r => r.url().endsWith(blocked ? "/key/block" : "/key/unblock") && r.request().method() === "POST");
        await page.getByRole("dialog").getByRole("button", { name: tr(blocked ? "Block" : "Unblock"), exact: true }).click();
        expect(await (await response).json()).toEqual({ blocked });
        await expect(page.getByText(tr(blocked ? "Key blocked" : "Key unblocked"), { exact: true })).toBeVisible();
        await expect(page.getByRole("dialog")).toHaveCount(0);
        await verifyBackend(blocked);
      }
    } finally {
      await page.unrouteAll({ behavior: "wait" });
      const deleted = await page.request.post(GATEWAY + "/key/delete", { headers, data: { key: id } });
      expect(deleted.status(), await deleted.text()).toBe(200);
    }
  });
}

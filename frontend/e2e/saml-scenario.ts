import {exerciseSAMLRefresh} from "./saml-refresh-scenario";
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { translate, type Locale } from "../src/i18n/translate";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册真实 SAML 浏览器流程；locale 与 secure 选择界面语言和控制台协议，mode 选择合法或拒绝路径。
 * refresh 额外验证在途登录期间刷新元数据及错误恢复，不用 mock 代替 IdP 下载。
 * 使用独立 SCIM 用户、本地 HTTP/TLS IdP、真实 Next /auth 代理及后台会话；finally 删除本例配置和身份，外层脚本回收 schema。 */
export function registerSAMLScenario(locale: Locale, secure: boolean, mode: "valid" | "unknown" | "tamper" | "replay" | "expired", refresh = false) {
  test(`${refresh ? "SAML refresh" : "SAML"} ${secure ? "HTTPS" : "HTTP"} ${mode} ${locale}`, async ({ page, browser, baseURL }) => {
    test.setTimeout(120_000);
    const f = JSON.parse(fs.readFileSync(path.join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8"));
    const tr = (key: string, vars?: Record<string, unknown>) => translate(locale, key, vars);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const provision = await page.request.post(f.control + "/identity");
    expect(provision.status(), await provision.text()).toBe(200);
    const identity = await provision.json();
    const origin = secure ? f.saml_console : new URL(baseURL!).origin;
    const name = `SAML ${secure ? "HTTPS" : "HTTP"} ${mode} ${locale}`;
    const context = await browser.newContext({ ignoreHTTPSErrors: true, acceptDownloads: true });
    await context.addCookies([{ name: "xhub_locale", value: locale, url: origin }]);
    const visitor = await context.newPage();
    try {
      await stableGoto(page, "/admin-panel");
      await page.getByRole("tab", { name: tr("enterprise.tabs.sso"), exact: true }).click();
      await page.getByRole("button", { name: tr("enterprise.configureSSO"), exact: true }).click();
      const form = page.getByRole("dialog", { name: tr("enterprise.configureSSO"), exact: true });
      await form.getByLabel(tr("directory.organization")).selectOption(identity.organization_id);
      await form.getByLabel(tr("enterprise.ssoName")).fill(name);
      await form.getByLabel(tr("enterprise.provider")).selectOption("saml");
      await expect(form.getByRole("heading", { name: tr("enterprise.samlTitle") })).toBeVisible();
      await expect(form.getByLabel(tr("enterprise.identity_claim"))).toHaveValue("urn:oid:0.9.2342.19200300.100.1.1");
      await expect(form.getByRole("switch", { name: tr("enterprise.allowHTTP") })).not.toBeChecked();
      await form.getByLabel(tr("enterprise.idp_metadata_url")).fill("");
      await form.getByLabel(tr("enterprise.idp_metadata"), { exact: true }).fill(mode === "expired" ? f.saml_metadata_expired : secure ? f.saml_metadata : f.saml_metadata_http);
      await form.getByLabel(tr("enterprise.publicURL")).fill(origin);
      if (!secure) {
        await expect(form.getByRole("button", { name: tr("enterprise.generateSAMLURLs") })).toBeDisabled();
        await form.getByRole("switch", { name: tr("enterprise.allowHTTP") }).check();
      }
      await form.getByRole("button", { name: tr("enterprise.generateSAMLURLs") }).click();
      await expect(form.getByLabel(tr("enterprise.redirect_uri"))).toHaveValue(origin + "/auth/enterprise/saml/acs");
      await expect(form.getByLabel(tr("enterprise.return_uri"), { exact: true })).toHaveValue(origin + "/ui/login");
      await form.getByLabel(tr("enterprise.samlEntityID")).fill("urn:xhub:browser:" + identity.connection_id);
      const saved = page.waitForResponse(r => r.url().includes("/admin/enterprise/sso") && r.request().method() === "POST");
      await form.getByRole("button", { name: tr("enterprise.save") }).click();
      const created = await saved;
      expect(created.status(), await created.text()).toBe(201);
      expect(await created.json()).toMatchObject({ protocol: "saml", allow_http: !secure, identity_claim: "urn:oid:0.9.2342.19200300.100.1.1" });
      await expect(form).not.toBeVisible();
      await page.getByRole("button", { name: tr("enterprise.configureName", { name }), exact: true }).click();
      const edit = page.getByRole("dialog", { name: tr("enterprise.configureSSO"), exact: true });
      if (mode === "expired") await expect(edit.getByText(tr("enterprise.samlCertificateExpired"), { exact: true })).toBeVisible();
      const downloadPromise = page.waitForEvent("download");
      await edit.getByRole("button", { name: tr("enterprise.downloadMetadata") }).click();
      const download = await downloadPromise;
      const downloaded = await download.path();
      expect(downloaded).toBeTruthy();
      const xml = fs.readFileSync(downloaded!, "utf8");
      expect(xml).toContain(origin + "/auth/enterprise/saml/acs");
      expect(xml).not.toContain("PRIVATE KEY");
      expect((await page.request.post(f.control + "/saml/register", { data: xml, headers: { "Content-Type": "application/xml" } })).status()).toBe(200);
      await page.keyboard.press("Escape");
      await expect(edit).not.toBeVisible();
      const toggle = page.getByRole("switch", { name: tr("enterprise.ssoEnableName", { name }) });
      if (mode === "expired") {
        const refused = page.waitForResponse(r => r.url().includes("/admin/enterprise/sso/") && r.request().method() === "PUT");
        await toggle.click();
        const failed = await refused;
        expect(failed.status(), await failed.text()).toBe(400);
        await expect(toggle).not.toBeChecked();
        await expect(page.getByText(tr("enterprise.samlCertificateExpired"), { exact: true }).first()).toBeVisible();
        await visitor.goto(origin + "/ui/login/");
        await expect(visitor.getByRole("link").filter({ hasText: name })).toHaveCount(0);
        // 默认拒绝后显式开启单连接例外，真实签名登录仍经同一浏览器及后台链路。
        await page.getByRole("button", { name: tr("enterprise.configureName", { name }), exact: true }).click();
        const exceptionForm = page.getByRole("dialog", { name: tr("enterprise.configureSSO"), exact: true });
        await expect(exceptionForm.getByRole("switch", { name: tr("enterprise.allowExpiredIDPCertificate") })).not.toBeChecked();
        await exceptionForm.getByRole("switch", { name: tr("enterprise.allowExpiredIDPCertificate") }).check();
        const exceptionSaved = page.waitForResponse(r => r.url().includes("/admin/enterprise/sso/") && r.request().method() === "PUT");
        await exceptionForm.getByRole("button", { name: tr("enterprise.save") }).click();
        expect((await exceptionSaved).status()).toBe(200);
        await expect(exceptionForm).not.toBeVisible();
      }
      const enabled = page.waitForResponse(r => r.url().includes("/admin/enterprise/sso/") && r.request().method() === "PUT");
      await toggle.click();
      const enableResult = await enabled;
      expect(enableResult.status(), await enableResult.text()).toBe(200);
      await expect(toggle).toBeChecked();
      expect((await page.request.post(f.control + "/saml/mode?value=" + (mode === "replay" || mode === "expired" ? "valid" : mode))).status()).toBe(200);
      await visitor.goto(origin + "/ui/login/");
      await visitor.getByRole("link").filter({ hasText: name }).click();
      await expect(visitor.getByLabel("Employee")).toBeVisible();
      if (refresh) await exerciseSAMLRefresh(page,{locale,name,id:identity.connection_id,source:f.saml_refresh_source+(mode === "expired" ? "/expired" : "/http"),headers});
      const acsPromise = visitor.waitForResponse(r => new URL(r.url()).pathname === "/auth/enterprise/saml/acs");
      const finishPromise = mode !== "tamper" ? visitor.waitForResponse(r => new URL(r.url()).pathname === "/auth/enterprise/saml/finish") : null;
      await visitor.getByRole("button", { name: "Sign in to identity provider" }).click();
      const acs = await acsPromise;
      if (mode === "tamper") {
        expect(acs.status(), await acs.text()).toBe(401);
        expect((await context.cookies()).some(c => c.name === "token")).toBe(false);
      } else {
        expect(acs.status()).toBe(303);
        // 跨站 POST 后必须经同站 GET 才发登录 Cookie；浏览器真实处理 SameSite，不用测试注入 Cookie。
        expect(acs.headers()["set-cookie"] || "").not.toContain("token=");
        const finish = await finishPromise!;
        if (mode === "unknown") {
          expect(finish.status(), await finish.text()).toBe(401);
          expect((await context.cookies()).some(c => c.name === "token")).toBe(false);
        } else {
          expect(finish.status()).toBe(302);
          await expect(visitor.getByText(tr("nav.apiKeys"), { exact: true }).first()).toBeVisible();
          const token = (await context.cookies()).find(c => c.name === "token");
          expect(token?.secure).toBe(secure);
          const claims = JSON.parse(Buffer.from(token!.value.split(".")[1], "base64url").toString());
          const session = { Authorization: "Bearer " + claims.key };
          const info = await visitor.request.get(GATEWAY + "/user/info", { headers: session });
          expect(info.status(), await info.text()).toBe(200);
          expect((await info.json()).user_info.user_id).toBe(identity.user.id ?? identity.user.user_id);
          const denied = await visitor.request.get(GATEWAY + "/audit/logs", { headers: session });
          expect(denied.status(), await denied.text()).toBe(403);
          if (mode === "replay") {
            const captured = await (await page.request.get(f.control + "/saml/last?http=" + !secure)).json();
            const replayContext = await browser.newContext({ ignoreHTTPSErrors: true });
            try {
              const replayPage = await replayContext.newPage();
              await replayPage.goto(origin + "/ui/login/");
              const rejected = replayPage.waitForResponse(r => new URL(r.url()).pathname === "/auth/enterprise/saml/acs");
              // 新浏览器实际提交旧 POST binding；浏览器使用本轮域名解析规则，不借 APIRequestContext 绕过页面导航。
              await replayPage.evaluate((value) => {
                const form = document.createElement("form");
                form.method = "POST"; form.action = value.url;
                for (const name of ["SAMLResponse", "RelayState"] as const) {
                  const input = document.createElement("input");
                  input.name = name; input.value = value[name]; form.appendChild(input);
                }
                document.body.appendChild(form); form.submit();
              }, captured);
              expect((await rejected).status()).toBe(401);
              await replayPage.waitForURL(u => u.pathname === "/auth/enterprise/saml/acs", { waitUntil: "domcontentloaded" });
              const repeat = await replayPage.goto(finish.url());
              expect(repeat!.status()).toBe(401);
              expect((await replayContext.cookies()).some(c => c.name === "token")).toBe(false);
            } finally {
              await replayContext.close();
            }
          }
        }
      }
    } finally {
      await context.close();
      await page.request.post(f.control + "/saml/mode?value=valid");
      expect([204, 404]).toContain((await page.request.delete(GATEWAY + "/admin/enterprise/sso/" + identity.connection_id, { headers })).status());
      expect((await page.request.delete(GATEWAY + "/admin/directory/connections/" + identity.connection_id, { headers })).status()).toBe(204);
      const cleaned = await page.request.post(f.control + "/identity/cleanup?organization_id=" + identity.organization_id);
      expect(cleaned.status(), await cleaned.text()).toBe(200);
    }
  });
}

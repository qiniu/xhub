import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { GATEWAY, loginAdmin, sessionBearer, stableGoto } from "../../helpers";
import { translate } from "../../../src/i18n/translate";

// 双语真实闭环：页面绑定/解绑、TLS IdP 登录、用户密钥推理和会话失效；finally 回收隔离组织、连接及用户。
for (const locale of ["en", "zh-CN"] as const) test("enterprise identity lifecycle " + locale, async ({ page, browser }) => {
  test.setTimeout(120_000);
  const tr = (key: string) => translate(locale, key);
  const f = JSON.parse(fs.readFileSync(path.join(process.env.E2E_RUN_DIR!, "enterprise-fixture.json"), "utf8"));
  await loginAdmin(page);
  await page.getByRole("button", { name: locale === "en" ? "English" : "中文", exact: true }).click();
  const headers = { Authorization: "Bearer " + await sessionBearer(page) };
  const orgResponse = await page.request.post(GATEWAY + "/organization/new", { headers, data: { organization_alias: "manual-identity-" + locale } });
  expect(orgResponse.status(), await orgResponse.text()).toBe(200);
  const org = await orgResponse.json();
  let connectionID = "", userID = "";
  const extraUsers: string[] = [];
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  try {
    const configured = await page.request.post(GATEWAY + "/admin/enterprise/sso", { headers, data: {
      organization_id: org.organization_id, name: "Manual identity " + locale, protocol: "oidc", issuer: f.issuer,
      client_id: "xhub-browser", client_secret: "local-browser-secret", identity_claim: "sub",
      redirect_uri: f.console + "/auth/enterprise/callback", return_uri: f.console + "/ui/login", enabled: true,
    } });
    expect(configured.status(), await configured.text()).toBe(201); connectionID = (await configured.json()).connection_id;
    // 真实 CSV 导入包含身份字段，预览后提交并通过用户详情接口确认持久化；finally 删除导入账号。
    await stableGoto(page, "/users");
    await page.getByRole("button", { name: tr("userCsv.title"), exact: true }).click();
    const dialog = page.getByRole("dialog");
    const csv = "user_email,user_role,password,sso_connection_id,sso_external_id\n" +
      "csv-identity-" + locale + "@example.invalid,user,csv-identity-password," + connectionID + ",csv-employee-" + locale;
    await expect(dialog.getByLabel(tr("userCsv.upload"))).toBeEnabled();
    await dialog.getByLabel(tr("userCsv.upload")).setInputFiles({ name: "identities.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
    await expect(dialog.getByText("csv-employee-" + locale, { exact: false })).toBeVisible();
    const imported = page.waitForResponse(r => r.url().endsWith("/user/new") && r.request().method() === "POST");
    await dialog.getByRole("button", { name: translate(locale, "userCsv.create", { count: 1 }), exact: true }).click();
    const importedResponse = await imported;
    expect(importedResponse.status(), await importedResponse.text()).toBe(200);
    const importedID = (await importedResponse.json()).user_id; extraUsers.push(importedID);
    const importedInfo = await page.request.get(GATEWAY + "/user/info?user_id=" + importedID, { headers });
    expect((await importedInfo.json()).user_info.sso_external_id).toBe("csv-employee-" + locale);
    // 新建表单也通过真实后台原子建立绑定；保留企业原始账号值，后续编辑验证改绑。
    await stableGoto(page, "/users");
    await page.getByRole("button", { name: "+ " + tr("pages.users.invite"), exact: true }).click();
    const createDialog = page.getByRole("dialog");
    await createDialog.getByLabel(tr("pages.users.userEmail")).fill("manual-" + locale + "@example.invalid");
    await createDialog.getByLabel(tr("Initial password"), { exact: true }).fill("manual-identity-password");
    await createDialog.getByRole("combobox", { name: tr("enterpriseUserIdentity.connection") }).click();
    await page.getByRole("option", { name: "Manual identity " + locale, exact: true }).click();
    await createDialog.getByLabel(tr("enterpriseUserIdentity.externalID")).fill("initial-employee-" + locale);
    const creation = page.waitForResponse(r => r.url().endsWith("/user/new") && r.request().method() === "POST");
    await createDialog.getByRole("button", { name: tr("pages.users.invite"), exact: true }).click();
    const created = await creation;
    expect(created.status(), await created.text()).toBe(200); userID = (await created.json()).user_id;
    await stableGoto(page, "/users?user=" + userID);
    await page.getByRole("button", { name: tr("pages.users.editUser"), exact: true }).click();
    await page.getByRole("combobox", { name: tr("enterpriseUserIdentity.connection") }).click();
    await page.getByRole("option", { name: "Manual identity " + locale, exact: true }).click();
    await page.getByLabel(tr("enterpriseUserIdentity.externalID")).fill("manual-employee-" + locale);
    const save = page.waitForResponse(r => r.url().includes("/user/update") && r.request().method() === "POST");
    await page.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true }).click(); expect((await save).status()).toBe(200);
    const read = await page.request.get(GATEWAY + "/user/info?user_id=" + userID, { headers }); expect((await read.json()).user_info.sso_external_id).toBe("manual-employee-" + locale);
    const visitor = await context.newPage(); await page.request.post(f.control + "/mode?value=valid");
    await visitor.goto(f.console + "/ui/login/"); await visitor.getByRole("link").filter({ hasText: "Manual identity " + locale }).click();
    await visitor.getByLabel("Employee").fill("manual-employee-" + locale);
    const callback = visitor.waitForResponse(r => new URL(r.url()).pathname === "/auth/enterprise/callback");
    await visitor.getByRole("button", { name: "Sign in to identity provider" }).click(); expect((await callback).status()).toBe(302);
    await expect.poll(async () => (await context.cookies()).some(c => c.name === "token")).toBe(true);
    const token = (await context.cookies()).find(c => c.name === "token")!;
    const session = JSON.parse(Buffer.from(token.value.split(".")[1], "base64url").toString()).key;
    const memberHeaders = { Authorization: "Bearer " + session };
    const member = await visitor.request.get(GATEWAY + "/user/info", { headers: memberHeaders }); expect(member.status()).toBe(200); expect((await member.json()).user_info.user_id).toBe(userID);
    const minted = await visitor.request.post(GATEWAY + "/key/generate", { headers: memberHeaders, data: { key_alias: "manual-identity-e2e" } }); expect(minted.status(), await minted.text()).toBe(200);
    const key = await minted.json();
    const call = await visitor.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + key.key }, data: { model: "gpt-4o-mini", messages: [{ role: "user", content: "Verify enterprise identity access" }] } }); expect(call.status(), await call.text()).toBe(200); expect((await call.json()).choices[0].message.content).toBe("e2e-ok");
    await page.reload(); await page.getByRole("button", { name: tr("pages.users.editUser"), exact: true }).click();
    await expect(page.getByLabel(tr("enterpriseUserIdentity.externalID"))).toHaveValue("manual-employee-" + locale);
    await page.getByRole("combobox", { name: tr("enterpriseUserIdentity.connection") }).click(); await page.getByRole("option", { name: tr("enterpriseUserIdentity.unbound"), exact: true }).click();
    await expect(page.getByLabel(tr("enterpriseUserIdentity.externalID"))).toHaveValue("");
    const cleared = page.waitForResponse(r => r.url().includes("/user/update") && r.request().method() === "POST");
    await page.getByRole("button", { name: tr("pages.users.saveChanges"), exact: true }).click(); expect((await cleared).status()).toBe(200);
    expect((await visitor.request.get(GATEWAY + "/user/info", { headers: memberHeaders })).status()).toBe(401);
    await context.clearCookies(); await visitor.goto(f.console + "/ui/login/"); await visitor.getByRole("link").filter({ hasText: "Manual identity " + locale }).click(); await visitor.getByLabel("Employee").fill("manual-employee-" + locale);
    const refused = visitor.waitForResponse(r => new URL(r.url()).pathname === "/auth/enterprise/callback"); await visitor.getByRole("button", { name: "Sign in to identity provider" }).click(); expect((await refused).status()).toBe(401);
  } finally {
    await context.close();
    for (const id of extraUsers) expect((await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: id } })).status()).toBe(200);
    if (userID) expect((await page.request.post(GATEWAY + "/user/delete", { headers, data: { user_id: userID } })).status()).toBe(200);
    if (connectionID) { expect((await page.request.delete(GATEWAY + "/admin/enterprise/sso/" + connectionID, { headers })).status()).toBe(204); expect([204, 404]).toContain((await page.request.delete(GATEWAY + "/admin/directory/connections/" + connectionID, { headers })).status()); }
    // 目录锚点按现有设计保留，组织受外键保护；运行器 finally 删除整个专属 schema 回收组织与锚点。

  }
});

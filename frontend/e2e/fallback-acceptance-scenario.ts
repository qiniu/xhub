import { execFileSync } from "node:child_process";

import { expect, test } from "@playwright/test";

import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto, t } from "./helpers";
/** 用途：注册独立场景「acceptance fallback excludes both shared-model healthy deployments」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC025() {


test("acceptance fallback excludes both shared-model healthy deployments", async ({ page }) => {
  test.setTimeout(120_000);
  await loginAdmin(page);
  const headers = { Authorization: "Bearer " + (await sessionBearer(page)) };
  const suffix = Date.now().toString();
  const primary = "acceptance-shared-" + suffix, backup = "acceptance-glm-" + suffix;
  const name = "acceptance-fallback-" + suffix;
  const ids = ["fault-", "fenno-", "qiniu-", "glm-"].map((prefix) => prefix + suffix);
  const upstreams = ["e2e-fallback-429-" + suffix, "healthy-fenno-" + suffix, "healthy-qiniu-" + suffix, "good-glm-" + suffix];
  let templateID = "";
  const keys: string[] = [];
  try {
    for (let i = 0; i < ids.length; i++) {
      const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
        model_name: i === 3 ? backup : primary,
        litellm_params: { model: upstreams[i], api_base: UPSTREAM, api_key: "sk-fake",
          custom_llm_provider: "custom", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
        model_info: { id: ids[i], transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual" },
      } });
      expect(created.status(), await created.text()).toBe(200);
    }
    // 调用本次修复的Python生成器，确保浏览器实际验证验收脚本产出的正文。
    const body = JSON.parse(execFileSync("python3", ["-c",
      "import json,sys; sys.path.insert(0,'e2e'); from acceptance import fallback_acceptance_deployments,fallback_acceptance_route; d=json.load(sys.stdin); healthy,target=fallback_acceptance_deployments(d['deployments'],d['primary'],d['backup'],'qiniu'); print(json.dumps(fallback_acceptance_route(d['primary'],d['fault'],healthy,d['backup'],target,60)))"],
      { cwd: "..", encoding: "utf8", input: JSON.stringify({ primary, backup, fault: ids[0], deployments: [
        { id: ids[2], public_name: primary, provider: "qiniu" },
        { id: ids[1], public_name: primary, provider: "fennoai" },
        { id: ids[3], public_name: backup, provider: "qiniu" },
      ] }) }));
    await stableGoto(page, "/route-templates");
    await page.getByRole("button", { name: t("pages.routeTemplates.create"), exact: true }).first().click();
    const editor = page.getByRole("region", { name: t("pages.routeTemplates.create"), exact: true });
    await editor.getByLabel(t("pages.routeTemplates.name"), { exact: true }).fill(name);
    await editor.getByRole("tab", { name: "JSON", exact: true }).click();
    await editor.getByRole("textbox", { name: "JSON", exact: true }).fill(JSON.stringify(body, null, 2));
    const saved = page.waitForResponse((r) => new URL(r.url()).pathname === "/route_template/new" && r.request().method() === "POST");
    await editor.getByRole("button", { name: "保存模板", exact: true }).click();
    const response = await saved;
    expect(response.status(), await response.text()).toBe(200);
    templateID = (await response.json()).id;
    const persisted = await (await page.request.get(GATEWAY + "/route_template/" + templateID, { headers })).json();
    expect(persisted.body.model_routes).toEqual(body.model_routes);
    expect(persisted.body.fallbacks).toEqual([{ [primary]: [backup] }, { [backup]: [] }]);
    const teams = await (await page.request.get(GATEWAY + "/v2/team/list?page=1&page_size=500", { headers })).json();
    const team = teams.teams.find((row: any) => row.team_alias === "e2e-fixture-team");
    const jwt = (await page.context().cookies()).find((cookie) => cookie.name === "token")!.value;
    const userID = JSON.parse(Buffer.from(jwt.split(".")[1], "base64url").toString()).user_id;
    for (const models of [[primary, backup], [primary]]) {
      const minted = await page.request.post(GATEWAY + "/key/generate", { headers, data: {
        key_alias: name + "-" + keys.length, owner_type: "personal", user_id: userID, team_id: team.team_id, models, route_template_id: templateID,
      } });
      expect(minted.status(), await minted.text()).toBe(200);
      keys.push((await minted.json()).key);
    }
    const request = { model: primary, session_id: "fallback-session-" + suffix, messages: [{ role: "user", content: "repeat acceptance fallback " + suffix }] };
    for (let repeat = 0; repeat < 2; repeat++) {
      // 相同正文验证首次真实回退及随后缓存；当前目标权限仍须逐次复核。
      const before = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
      const call = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + keys[0] }, data: request });
      expect(call.status(), await call.text()).toBe(200);
      expect(call.headers()["cache_hit"] === "true").toBe(repeat > 0);
      expect(call.headers()["x-litellm-cache-hit"] === "true").toBe(repeat > 0);
      // 第一次故障加回退共两次外发，下一轮复用成功正文且不产生外发。
      const after = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
      expect(after - before, "首次回退与成功后复用的真实调用次数").toBe(repeat === 0 ? 2 : 0);
      expect(call.headers()["x-session-id"]).toBe(request.session_id);
      const answer = await call.json();
      expect(answer.choices[0].message.content).toBe("e2e-ok");
      expect(answer.model).toBe(primary);
      const callID = call.headers()["x-litellm-call-id"];
      expect(callID).toBeTruthy();
      await expect.poll(async () => {
        const response = await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers });
        if (!response.ok()) return false;
        const log = await response.json();
        return repeat === 0 ? Number(log.spend) > 0 : log.cache_hit === "true" && Number(log.spend) === 0;
      }).toBe(true);
      const log = await (await page.request.get(GATEWAY + "/spend/logs/ui/" + callID, { headers })).json();
      expect(log.model).toBe(primary);
      expect(log.session_id).toBe(request.session_id);
      expect(log.session_source).toBe("explicit");
    }
    const beforeRevocation = (await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests;
    expect((await page.request.post(GATEWAY + "/key/update", { headers, data: { key: keys[0], models: [primary] } })).status()).toBe(200);
    const cachedDenied = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + keys[0] }, data: request });
    expect(cachedDenied.status(), await cachedDenied.text()).toBe(401);
    expect((await (await page.request.get(UPSTREAM + "/e2e/request-count")).json()).requests).toBe(beforeRevocation);
    expect((await page.request.post(GATEWAY + "/key/update", { headers, data: { key: keys[0], models: [primary, backup] } })).status()).toBe(200);
    const denied = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + keys[1] }, data: request });
    expect(denied.status(), await denied.text()).toBe(401);
    expect(await denied.text()).toContain("model not in allowed model list");
    const disabled = await page.request.post(GATEWAY + "/v1/chat/completions", { headers: { Authorization: "Bearer " + keys[0] }, data: { ...request, disable_fallbacks: true } });
    expect(disabled.status(), await disabled.text()).toBe(429);
    const deletedKeys = await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys } });
    expect(deletedKeys.status(), await deletedKeys.text()).toBe(200);
    keys.length = 0;
    await page.reload();
    const row = page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
    await row.getByRole("button", { name: name + " 的操作" }).click();
    await page.getByRole("menuitem", { name: t("pages.routeTemplates.delete"), exact: true }).click();
    const deleted = page.waitForResponse((r) => new URL(r.url()).pathname === "/route_template/" + templateID + "/delete");
    await page.getByRole("dialog").getByRole("button", { name: t("pages.routeTemplates.delete"), exact: true }).click();
    expect((await deleted).status()).toBe(200);
    await expect(row).toHaveCount(0);
    templateID = "";
  } finally {
    if (keys.length) await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys } });
    if (templateID) await page.request.post(GATEWAY + "/route_template/" + templateID + "/delete", { headers, data: {} });
    for (const id of ids) await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } });
  }
});
}

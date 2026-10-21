import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { translate } from "../src/i18n/translate";
import { GATEWAY, UPSTREAM, loginAdmin, sessionBearer, stableGoto } from "./helpers";

/** 注册平台管理员的双语人工对账全链路；参数为语言，无返回值，供独立登记场景调用。 */
export function registerBudgetReconciliationScenario(locale: "en" | "zh-CN") {
  /** 前置隔离PG、真实浏览器和缺失流终态的本地上游；页面创建有限额Key，截断流完成后注入升级前遗留的uncertain夹具。
   * 验证额度被占、API Key不能管理、人工无收费/实际收费、审计回执、唯一差额、余额恢复和并发冲突。
   * finally删除自建Key及部署，隔离schema由运行器清理；不调用外部供应商、不更改用户数据。 */
  test("manual budget reconciliation restores verified balance " + locale, async ({ page }) => {
    test.setTimeout(120000);
    await loginAdmin(page);
    if (locale === "en") await page.getByRole("button", { name: "English", exact: true }).click();
    const tr = (key: string, values?: Record<string, string | number>) => translate(locale, key, values);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const name = "reconcile-" + locale + "-" + Date.now();
    const models: string[] = []; let keyID = "", key = "";
    try {
      for (const kind of ["missing", "length"]) {
        const created = await page.request.post(GATEWAY + "/model/new", { headers, data: {
          model_name: name + "-" + kind,
          litellm_params: { model: "e2e-stream-terminal-" + kind, custom_llm_provider: "openai", api_base: UPSTREAM, api_key: "sk-fake", input_cost_per_token: 0.000001, output_cost_per_token: 0.000002 },
          model_info: { transport: "bypass_openai_chat", endpoint_types: ["chat"], pricing_source: "manual", max_input_tokens: 100, max_output_tokens: 64, supported_parameters: ["max_tokens"] },
        } });
        expect(created.status(), await created.text()).toBe(200); models.push((await created.json()).model_info.id);
      }
      await page.getByTestId("create-key-button").click();
      await page.getByLabel(tr("Key Name")).fill(name);
      await page.getByLabel(tr("Max Budget (USD)"), { exact: true }).fill("0.0003");
      const minted = page.waitForResponse(r => r.url().includes("/key/generate") && r.request().method() === "POST");
      await page.getByRole("button", { name: tr("pages.apiKeys.createSubmit"), exact: true }).click();
      const createdKey = await (await minted).json(); keyID = createdKey.token_id; key = createdKey.key;
      await page.keyboard.press("Escape");
      const keyHeaders = { Authorization: "Bearer " + key };
      /** 实际调用本地声明模型；参数决定缺终态或正常长度终态，返回真实HTTP响应，不直接改账本。 */
      const invoke = (kind: string) => page.request.post(GATEWAY + "/v1/chat/completions", { headers: keyHeaders, data: { model: name + "-" + kind, stream: true, max_tokens: 64, messages: [{ role: "user", content: "Return the terminal test marker." }] } });
      /** 查询当前Key可操作预占；返回后台当前列表，不将未完成落账误认为可核对。 */
      const pending = async () => {
        const response = await page.request.get(GATEWAY + "/budget/reservations?key_id=" + keyID + "&state=uncertain&page=1&page_size=50", { headers });
        expect(response.status(), await response.text()).toBe(200);
        return (await response.json()).reservations as { reservation_id: string; call_id: string; fingerprint: string; can_reconcile: boolean; amount: number; ledger_cost: number | null }[];
      };
      /** 为当前隔离 schema 的已记录请求补一条旧版预占，模拟升级数据；不触碰真实环境，最终随 schema 清理。 */
      const seedLegacyHold = async (callID: string) => {
        expect(await pending()).toHaveLength(0);
        const schema = fs.readFileSync(path.join(process.env.E2E_RUN_DIR!, "schema.txt"), "utf8").trim();
        if (!/^e2e_\d+_\d+$/.test(schema) || !/^[a-f0-9]+$/.test(callID)) throw new Error("Invalid isolated fixture identity");
        const sql = `SET search_path TO "${schema}";
          INSERT INTO budget_reservations (request_id,call_id,kind,node_id,key_id,user_id,team_id,org_id,project_id,amount,snapshot,state,reason,started_at,created_at)
          SELECT 'legacy-' || request_id,request_id,'key',key_id,key_id,user_id,team_id,organization_id,project_id,0.0003,'{}','uncertain','Legacy test invoice',ts,NOW()
          FROM usage_events WHERE request_id='${callID}' RETURNING request_id;`;
        const inserted = execFileSync("docker", ["exec", "-i", "xhub-postgres", "psql", "-X", "-U", "xhub", "-d", process.env.E2E_DATABASE || "xhub", "-qAt", "-v", "ON_ERROR_STOP=1"], {input:sql,encoding:"utf8"});
        expect(inserted).toContain("legacy-" + callID);
      };
      let expectedSpend = 0;
      for (const outcome of ["no_charge", "charged"] as const) {
        const broken = await invoke("missing");
        expect(broken.status()).toBe(200); expect(await broken.text()).toContain("TERMINAL_BROWSER_OK");
        await seedLegacyHold(broken.headers()["x-litellm-call-id"]);
        await expect.poll(async () => (await pending()).filter(row => row.can_reconcile).length).toBe(1);
        const row = (await pending())[0];
        expect(row.amount).toBeGreaterThan(0); expect((await invoke("length")).status()).toBe(429);
        const beforeReconcileSpend = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info.spend;
        const denied = await page.request.get(GATEWAY + "/budget/reservations?key_id=" + keyID, { headers: keyHeaders });
        expect(denied.status()).toBe(403);
        await stableGoto(page, "/api-keys?key=" + keyID);
        const region = page.getByRole("region", { name: tr("budgetReconciliation.title"), exact: true });
        await expect(region.getByText(row.call_id, { exact: true })).toBeVisible();
        await region.getByRole("button", { name: tr("budgetReconciliation.openAttempt", { attempt: row.reservation_id }) }).click();
        const dialog = page.getByRole("dialog", { name: tr("budgetReconciliation.confirmTitle") });
        await expect(dialog.getByText(tr("budgetReconciliation.deltaHelp"), { exact: true })).toBeVisible();
        await dialog.getByRole("button", { name: tr("budgetReconciliation.confirm"), exact: true }).click();
        await expect(dialog.getByRole("alert")).toHaveText(tr("budgetReconciliation.invalidEvidence"));
        await dialog.getByLabel(tr("budgetReconciliation.outcome")).selectOption(outcome);
        const actual = outcome === "charged" ? 0.000025 : 0;
        if (outcome === "charged") await dialog.getByLabel(tr("budgetReconciliation.cost")).fill(String(actual));
        const evidence = "Synthetic supplier invoice " + outcome + ": manually verified.";
        await dialog.getByLabel(tr("budgetReconciliation.evidence")).fill(evidence);
        // 发布控制台经 /gw 代理，开发模式直连；以相同业务路径后缀匹配，保留真实请求和响应断言。
        const submitted = page.waitForResponse(r => new URL(r.url()).pathname.endsWith("/budget/reservations/" + row.reservation_id + "/reconcile") && r.request().method() === "POST");
        await dialog.getByRole("button", { name: tr("budgetReconciliation.confirm"), exact: true }).click();
        const completed = await submitted; expect(completed.status(), await completed.text()).toBe(200);
        const result = await completed.json();
        expect(result.resolution).toMatchObject({ reservation_id: row.reservation_id, outcome, verified_cost_usd: actual, evidence });
        expect(result.resolution.cost_delta_usd).toBe(actual);
        expect(result.resolution.previous_cost_usd).toBe(row.ledger_cost);
        expect(result.resolution.updated_cost_usd).toBeCloseTo((row.ledger_cost ?? 0) + actual, 9);
        expect(result.resolution.actor_id).toBeTruthy(); expect(result.resolution.resolved_at).toBeTruthy();
        await expect(region.getByText(tr("budgetReconciliation.evidenceSaved", { evidence }), { exact: true })).toBeVisible();
        await expect(page.getByText(tr("hardBudget.pending", { amount: 0 }), { exact: true })).toBeVisible();
        const again = await page.request.post(GATEWAY + "/budget/reservations/" + row.reservation_id + "/reconcile", { headers, data: completed.request().postDataJSON() });
        expect(again.status(), await again.text()).toBe(200); expect((await again.json()).already_resolved).toBe(true);
        // 核定的是本attempt费用；保留同一调用其他已计费尝试，不能把call累计金额当退款抵扣。
        expectedSpend = beforeReconcileSpend + actual;
        const restored = await invoke("length"); expect(restored.status(), await restored.text()).toBe(200); expect(await restored.text()).toContain("TERMINAL_BROWSER_OK");
        expectedSpend += 0.000012;
        await expect.poll(async () => {
          const info = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info;
          return { spend: Number(Number(info.spend).toFixed(9)), pending: Number(info.budget_pending || 0) };
        }).toEqual({ spend: Number(expectedSpend.toFixed(9)), pending: 0 });
      }
      // 在已打开的确认表单外完成同笔对账，随后提交不同结果必须真实409；不可把并发成功当作本次收费。
      const lastBroken = await invoke("missing");
      await seedLegacyHold(lastBroken.headers()["x-litellm-call-id"]);
      await expect.poll(async () => (await pending()).filter(row => row.can_reconcile).length).toBe(1);
      const concurrent = (await pending())[0];
      const concurrentSpend = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info.spend;
      await stableGoto(page, "/api-keys?key=" + keyID);
      await page.getByRole("button", { name: tr("budgetReconciliation.openAttempt", { attempt: concurrent.reservation_id }) }).click();
      const external = await page.request.post(GATEWAY + "/budget/reservations/" + concurrent.reservation_id + "/reconcile", { headers, data: { outcome: "no_charge", verified_cost_usd: 0, evidence: "Concurrent verified invoice.", expected_fingerprint: concurrent.fingerprint } });
      expect(external.status(), await external.text()).toBe(200);
      expectedSpend = concurrentSpend;
      const dialog = page.getByRole("dialog", { name: tr("budgetReconciliation.confirmTitle") });
      await dialog.getByLabel(tr("budgetReconciliation.outcome")).selectOption("charged");
      await dialog.getByLabel(tr("budgetReconciliation.cost")).fill("0.0001");
      await dialog.getByLabel(tr("budgetReconciliation.evidence")).fill("Stale client evidence.");
      await dialog.getByRole("button", { name: tr("budgetReconciliation.confirm"), exact: true }).click();
      await expect(dialog.getByRole("alert")).toHaveText(tr("budgetReconciliation.conflict"));
      await expect(dialog.getByRole("button", { name: tr("budgetReconciliation.confirm"), exact: true })).toBeDisabled();
      await dialog.getByRole("button", { name: tr("budgetReconciliation.reload") }).click();
      await expect(page.getByText(tr("budgetReconciliation.empty"), { exact: true })).toBeVisible();
      await expect(page.getByText(tr("hardBudget.pending", { amount: 0 }), { exact: true })).toBeVisible();
      const finalInfo = (await (await page.request.get(GATEWAY + "/key/info?key=" + keyID, { headers })).json()).info;
      expect(finalInfo.budget_pending).toBe(0); expect(finalInfo.spend).toBeCloseTo(expectedSpend, 9);
    } finally {
      if (keyID) expect((await page.request.post(GATEWAY + "/key/delete", { headers, data: { keys: [keyID] } })).ok()).toBeTruthy();
      for (const id of models) expect((await page.request.post(GATEWAY + "/model/delete", { headers, data: { id } })).ok()).toBeTruthy();
    }
  });
}

import { execFileSync } from "node:child_process";

import fs from "node:fs";

import os from "node:os";

import path from "node:path";

import { expect, test } from "@playwright/test";

import { GATEWAY, MASTER, loginAdmin, sessionBearer, stableGoto, t, watchGateway } from "./helpers";
/** 用途：注册独立场景「testdata 仅构造资源，控制台可见且不执行 Codex 或媒体模型」；无参数、无返回值。前置真实浏览器、私有数据库和本地上游；验证页面及原始契约，保留原套件前后置清理，运行器最终回收专属 schema。异常由 Playwright 单独记录。 */
export function registerSC244() {


test("testdata 仅构造资源，控制台可见且不执行 Codex 或媒体模型", async ({ page }) => {
  // 建数需要空租户环境，必须单独选择本文件；完整浏览器套件的其他用例会先创建组织。
  test.skip(process.env.E2E_TESTDATA_SEED !== "1", "请使用 E2E_TESTDATA_SEED=1 单独运行 scenarios/legacy/testdata-seed/sc-244.test.ts");
  test.setTimeout(120_000);
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "xhub-testdata-"));
  // 真实供应商验收由调用方提供私有凭据文件；默认回归只验证持久化，不能冒充供应商支持证明。
  const live = !!process.env.E2E_TESTDATA_CREDENTIALS_FILE;
  const credentials: Record<string, string> = live
    ? JSON.parse(fs.readFileSync(process.env.E2E_TESTDATA_CREDENTIALS_FILE!, "utf8"))
    : { FENNO_AI_API_KEY: "seed-fenno-secret", QINIU_API_KEY: "seed-qiniu-secret" };
  try {
    try {
      execFileSync("python3", [path.resolve(__dirname, "../../scripts/e2e.py"),
      "--gateway", GATEWAY, "--directory", directory, "--seed-only"], {
      env: { ...process.env, E2E_DATASET_ADMIN: "admin", E2E_DATASET_PASSWORD: MASTER,
        ...credentials }, timeout: 90_000,
      });
    } catch (error) {
      const reportPath = path.join(directory, "report.json");
      const detail = fs.existsSync(reportPath) ? JSON.parse(fs.readFileSync(reportPath, "utf8")).error : String(error);
      throw new Error("testdata 建数失败：" + detail);
    }
    const access = JSON.parse(fs.readFileSync(path.join(directory, "access.json"), "utf8"));
    const report = JSON.parse(fs.readFileSync(path.join(directory, "report.json"), "utf8"));
    expect(access.keys, "应构造 81 把成员个人密钥").toHaveLength(81);
    expect(access.admin_key).toMatchObject({ owner_type: "personal", key_alias: "管理员个人密钥", team_id: null });
    expect(report.total_key_count).toBe(82);
    expect(report.status).toBe("seeded");
    expect(report.calls).toEqual([]);
    expect(report.probe_attempts).toEqual([]);
    expect(report.agent_conversations).toBeUndefined();
    expect(report.media_tasks).toBeUndefined();
    const guard = watchGateway(page);
    await loginAdmin(page);
    const headers = { Authorization: "Bearer " + await sessionBearer(page) };
    const schema = fs.readFileSync(path.join(process.env.E2E_RUN_DIR!, "schema.txt"), "utf8").trim();
    expect(schema).toMatch(/^e2e_[0-9]+_[0-9]+$/);
    for (const [name, envName] of [["fennoai", "FENNO_AI_API_KEY"], ["qiniu", "QINIU_API_KEY"]]) {
      // SQL 不携带秘密，私有查询结果只在内存比较；断言布尔值，失败报告也不复述密钥。
      const saved = execFileSync("docker", ["exec", "-i", "xhub-postgres", "psql", "-X", "-U", "xhub", "-d", "xhub", "-qAt", "-v", "ON_ERROR_STOP=1"], {
        input: `SELECT body::jsonb->'credential_values'->>'api_key' FROM ${schema}.kv WHERE kind='credentials' AND id='${name}';`,
        encoding: "utf8",
      });
      expect(saved.trim() === credentials[envName], name + " 应持久化实际值").toBe(true);
    }
    await stableGoto(page, "/model-providers");
    await page.getByTestId("credential-actions-fennoai").click();
    await page.getByTestId("credential-action-edit").click();
    const editor = page.getByRole("dialog", { name: t("Edit model provider") });
    const displayed = await editor.getByLabel("OpenAI API Key").inputValue();
    expect(displayed.startsWith("os")).toBe(false);
    expect(displayed.includes("*")).toBe(true);
    expect(displayed === credentials.FENNO_AI_API_KEY).toBe(false);
    await editor.getByRole("button", { name: t("Cancel"), exact: true }).click();
    await stableGoto(page, "/api-keys");
    await expect(page.getByText("管理员个人密钥", { exact: true }).first()).toBeVisible();
    const logs = await page.request.get(GATEWAY + "/spend/logs/ui?page_size=200", {
      headers: { Authorization: "Bearer " + await sessionBearer(page) },
    });
    expect(logs.status()).toBe(200);
    expect((await logs.json()).data, "建数不应产生任何数据面调用日志").toEqual([]);
    await stableGoto(page, "/organizations");
    for (const organization of access.organizations) {
      await expect(page.getByText(organization.name, { exact: true }).first()).toBeVisible();
    }
    await stableGoto(page, "/models-and-endpoints");
    await page.getByRole("tab", { name: "全部模型", exact: true }).click();
    await expect(page.getByText("gpt-5.6-sol", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("z-ai/glm-5", { exact: true }).first()).toBeVisible();
    if (live) {
      // 网关进程没有供应商环境变量；从建数数据库读取密钥，真正发送 Responses 到 Fenno。
      const key = access.keys.find((row: { profile: string }) => row.profile === "fenno-only");
      const inference = await page.request.post(GATEWAY + "/v1/responses", {
        headers: { Authorization: "Bearer " + key.key },
        data: { model: key.call_model, input: "Reply with exactly: fenno-seeded-ok", max_output_tokens: 32 },
        timeout: 90_000,
      });
      expect(inference.status(), "真实 Fenno 数据面调用应成功").toBe(200);
      const response = await inference.json();
      expect(response.usage.total_tokens).toBeGreaterThan(0);
      const evidence = await page.request.get(GATEWAY + "/spend/logs/ui?page_size=200", { headers });
      expect(evidence.status()).toBe(200);
      expect((await evidence.json()).data.length).toBeGreaterThan(0);
    }
    await guard.assertOk();
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
}

import { expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

/** 验证隔离测试库已实际复用续聊块；无参数与返回值，仅在新格式测试开启时读取当前 schema，不访问用户数据。 */
export function expectCompactHistoryReuse() {
  if (process.env.XHUB_COMPACT_HISTORY !== "1") return;
  const schema = fs.readFileSync(path.join(process.env.E2E_RUN_DIR!, "schema.txt"), "utf8").trim();
  expect(schema).toMatch(/^e2e_[0-9]+_[0-9]+$/);
  const counts = execFileSync("docker", ["exec", "-i", "xhub-postgres", "psql", "-X", "-U", "xhub", "-d", process.env.E2E_DATABASE || "xhub", "-qAt", "-v", "ON_ERROR_STOP=1"], {
    input: `SET search_path TO "${schema}"; SELECT count(*) FROM (SELECT scope, sum(jsonb_array_length(digests)) AS refs FROM conversation_history_manifests GROUP BY scope) m JOIN (SELECT scope, count(*) AS blocks FROM conversation_history_blocks GROUP BY scope) b USING (scope) WHERE m.refs > b.blocks;`, encoding: "utf8",
  }).trim();
  expect(Number(counts)).toBeGreaterThan(0);
}

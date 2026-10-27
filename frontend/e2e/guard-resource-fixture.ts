import { test as base } from "@playwright/test";
import { GATEWAY } from "./helpers";
import { startProbeFixture, type ProbeFixture } from "./probe-fixture";
import { cleanupGuardResources } from "./guard-resource-cleanup.cjs";

type GuardResources = { id: string; modelID: string; headers: Record<string, string>; fixture: ProbeFixture };

/** 护栏测试独占资源夹具；Playwright 在 use 返回、抛错或测试超时后执行 teardown。
 * 独立 APIRequestContext 不依赖 page 生命周期；夹具具有自己的 30 秒预算，不提高业务测试的 60 秒门限。
 * 正常与异常都尝试清理护栏、模型、HTTP 监听，释放 API 上下文；清理失败向测试传播，不吞掉原测试失败。
 */
export const test = base.extend<{ guardResources: GuardResources }>({
  guardResources: [async ({ playwright }, runTest) => {
    const api = await playwright.request.newContext();
    try {
      const state: GuardResources = { id: "", modelID: "", headers: {}, fixture: await startProbeFixture() };
      try { await runTest(state); }
      finally { await cleanupGuardResources(state, api, GATEWAY); }
    } finally { await api.dispose(); }
  }, { timeout: 30_000 }],
});

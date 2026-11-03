import { afterEach, describe, expect, it, vi } from "vitest";
import { tagListCall } from "../networking";

afterEach(() => vi.unstubAllGlobals());
/** 客户端测试覆盖实际HTTP解析边界，fetch替身不用于证明数据库可用；每例恢复全局函数。 */
describe("tag list HTTP failures", () => {
  for (const status of [401, 403, 500, 503]) {
    /** 已知HTTP失败必须reject，不允许以{}冒充空目录；身份与底层错误文案交给共用客户端处理。 */
    it("rejects " + status, async () => {
      vi.stubGlobal(
        "fetch",
        vi
          .fn()
          .mockResolvedValue(
            new Response(JSON.stringify({ error: { type: "internal", message: "catalog store failure" } }), {
              status,
              headers: { "Content-Type": "application/json" },
            }),
          ),
      );
      await expect(tagListCall("token")).rejects.toMatchObject({ status });
    });
  }
  /** 正常真实记录不改形状；日期和鉴权头按既有请求契约发送。 */
  it("preserves dictionary, dates and auth", async () => {
    const result = { 中文: { name: "中文", models: [] } };
    const fetcher = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify(result), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    vi.stubGlobal("fetch", fetcher);
    expect(await tagListCall("token", new Date(2026, 9, 1), new Date(2026, 9, 2))).toEqual(result);
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toContain("/tag/list?start_date=2026-10-01&end_date=2026-10-02");
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer token");
  });
  /** 空配置是正常成功，单独传日期不发送不完整筛选；afterEach恢复fetch。 */
  it("preserves empty catalog without partial date filters", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetcher);
    expect(await tagListCall("token", new Date(2026, 9, 1))).toEqual({});
    expect(fetcher.mock.calls[0][0]).toMatch(/\/tag\/list$/);
  });
  /** HTTP成功但正文损坏仍须失败，不能制造空目录；afterEach恢复fetch。 */
  it("rejects invalid JSON", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("not JSON", { status: 200 })));
    await expect(tagListCall("token")).rejects.toThrow();
  });
  /** 网络中断不能变空列表；保留拒绝给组件，防止伪成功，afterEach清理替身。 */
  it("rejects network failure", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("network down")));
    await expect(tagListCall("token")).rejects.toThrow("network down");
  });
});

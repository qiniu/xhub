import { expect, it } from "vitest";
import { fullErrorDetails } from "./logErrors";

/** 回归验证：输入真实异步任务落库形状，完整错误应显示最终失败而不是初始排队响应；无外部数据或清理。 */
it("preserves async task failure instead of showing initial queued response", () => {
  const detail = fullErrorDetails({
    body_capture_status: "saved",
    error: '{"status":"failed","error":{"code":"render_failed","message":"Synthetic render failure."}}',
    response: { id: "video-test", status: "queued" },
  });
  expect(detail).toContain("render_failed");
});

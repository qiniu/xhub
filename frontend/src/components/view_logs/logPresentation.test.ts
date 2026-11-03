import { describe, expect, it } from "vitest";
import { logColumnVisibility, logOutcomeKey, logErrorSummary } from "./logPresentation";

describe("日志展示摘要", () => {
  /** 前置不同 HTTP 状态，包括缺失和异常输入；只描述现象不推断责任方，纯函数无需清理。 */
  it.each([
    [499, "interrupted"],
    [429, "limited"],
    [413, "bodyRejected"],
    [401, "denied"],
    [403, "denied"],
    [502, "serviceFailed"],
    [undefined, "requestFailed"],
    ["oops", "requestFailed"],
  ])("classifies %s neutrally", (http_status, key) => {
    expect(logOutcomeKey({ metadata: { http_status } })).toBe(`logsUX.${key}`);
  });
  /** 前置普通/错误列表；默认仅错误列表展示摘要，低频技术列可通过列菜单恢复，纯函数无需清理。 */
  it("uses different default columns for errors and usage", () => {
    expect(logColumnVisibility(true)).toMatchObject({
      diagnosis: true,
      spend: false,
      total_tokens: false,
      key_hash: false,
    });
    expect(logColumnVisibility(false)).toMatchObject({ diagnosis: false, spend: true, total_tokens: true });
  });
});

/** 前置 JSON、纯文本、未知格式和超长错误；摘要可读且有限长，原始日志不变，纯函数无需清理。 */
it("extracts a bounded error message without altering evidence", () => {
  expect(logErrorSummary({ error: '{"error":{"message":"Upstream unavailable"}}' })).toBe("Upstream unavailable");
  expect(logErrorSummary({ error: "dial tcp\nconnection refused" })).toBe("dial tcp connection refused");
  expect(logErrorSummary({ response: { detail: { code: 1 } } })).toBe("");
  expect(logErrorSummary({ error: "{broken" })).toBe("{broken");
  const log = { error: "x".repeat(1024) };
  expect(logErrorSummary(log)).toBe("x".repeat(240) + "…");
  expect(log.error).toHaveLength(1024);
  expect(
    logErrorSummary({ error: "ignored", metadata: { error_information: { error_message: "recorded summary" } } }),
  ).toBe("recorded summary");
});

import { describe, expect, it } from "vitest";
// Next配置为原生ESM，由框架读取；直接读取正式配置，避免复制测试专用默认值。
// @ts-expect-error 配置文件没有单独的TypeScript声明
import config from "../next.config.mjs";

/** 验证控制台缓存覆盖网关合法64MiB正文及超限探测余量；纯配置测试，无资源需清理。 */
describe("console request body buffering", () => {
  it("buffers admitted bodies and an extra chunk for the gateway 413 boundary", () => {
    expect(config.experimental.proxyClientMaxBodySize).toBe(65 * 1024 * 1024);
    expect(config.experimental.proxyTimeout).toBe(600000);
  });
});

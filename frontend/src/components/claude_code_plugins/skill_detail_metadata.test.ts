import { describe, expect, it } from "vitest";
import { skillDetailDate, skillDetailSourceLink } from "./skill_detail_metadata";
import type { PluginSource } from "./types";

/** 详情元数据纯函数测试；输入来源/日期，验证正常、缺省及失败边界，无网络或持久化清理。 */
describe("Skill detail metadata", () => {
  /** 子目录定位及其他来源保持原语义；不探测链接可达性，无外部副作用。 */
  it.each<[PluginSource | undefined, string | null]>([
    [{ source: "git-subdir", url: "https://github.com/example/repo", path: "skills/test" }, "https://github.com/example/repo/tree/main/skills/test"],
    [{ source: "git-subdir", url: "https://github.com/example/repo" }, "https://github.com/example/repo"],
    [{ source: "git-subdir", path: "skills/test" }, null],
    [{ source: "github", repo: "example/repo" }, "https://github.com/example/repo"],
    [{ source: "url", url: "https://example.com/source" }, "https://example.com/source"],
    [{ source: "archive", url: "https://example.com/skill.zip" }, "https://example.com/skill.zip"],
    [undefined, null],
  ])("保留来源链接 %#", (source, expected) => { expect(skillDetailSourceLink(source)).toBe(expected); });
  /** 固定本地时间避免测试机时区漂移；两种界面语言的格式不同，不改变原始日期字符串。 */
  it.each(["en", "zh-CN"] as const)("日期遵循界面语言 %s", locale => {
    const input = "2026-10-28T12:00:00";
    expect(skillDetailDate(input, locale)).toBe(locale === "en" ? "10/28/2026" : "2026/10/28");
  });
  /** 缺省和错误输入保留原文，不能展示伪造日期或抛错；无清理。 */
  it.each([undefined, "", "unparseable 用户日期"])("保留日期边界 %s", input => {
    expect(skillDetailDate(input, "en")).toBe(input);
  });
});

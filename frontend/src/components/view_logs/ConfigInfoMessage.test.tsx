import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { translate } from "@/i18n/translate";
import { ConfigInfoMessage } from "./ConfigInfoMessage";

describe("ConfigInfoMessage", () => {
  // 正文不可用时只能陈述原因未知，不能误报开关状态；无外部依赖，测试框架清理 DOM。
  it("shows a neutral missing-body explanation", () => {
    render(<ConfigInfoMessage show />);
    expect(screen.getByRole("note")).toHaveTextContent(translate("en", "logsUX.bodyUnavailable"));
    expect(screen.queryByText(/store_prompts_in_spend_logs/)).not.toBeInTheDocument();
  });
  // 隐藏状态不渲染提示，避免加载或失败期间误判；测试框架清理 DOM。
  it("renders nothing when hidden", () => {
    const { container } = render(<ConfigInfoMessage show={false} />);
    expect(container).toBeEmptyDOMElement();
  });
  // 中文与英文均描述未知原因，不向用户建议直接修改服务器配置；无外部依赖。
  it.each(["en", "zh-CN"] as const)("translates the neutral explanation in %s", (locale) => {
    const text = translate(locale, "logsUX.bodyUnavailable");
    expect(text).not.toBe("logsUX.bodyUnavailable");
    expect(text).not.toMatch(/proxy_config|store_prompts/);
  });
});

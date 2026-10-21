import { act, fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setActiveLocale } from "@/i18n";
import { apiClient } from "../networking";
import RoutePreview from "./RoutePreview";
vi.mock("../networking", () => ({ apiClient: { get: vi.fn(), post: vi.fn() } }));
const props = {
  accessToken: "test",
  models: ["用户模型"],
  body: { model_routes: [] },
  templateId: "template",
  organizationId: "org",
  teamId: "team",
};
const result = { source: "template", rule_source: "default", routing_strategy: "simple", data: [] };
/** 每例准备入口目录；仅模拟网络传输，卸载与清理 mock 避免跨用例污染。 */
beforeEach(() => {
  setActiveLocale("en");
  vi.mocked(apiClient.get).mockResolvedValue({ endpoint_types: [{ id: "chat", label: "Chat" }] });
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
/** 等待目录并选择原始模型；返回按钮，供点击真实组件行为，无后台副作用。 */
async function selectModel() {
  await screen.findByRole("option", { name: "Chat" });
  fireEvent.change(screen.getByLabelText("Preview public model"), { target: { value: "用户模型" } });
  return screen.getByRole("button", { name: "Preview draft route" });
}
/** 验证成功请求包含草稿和作用域，结果可见；空结果是成功状态，卸载清理。 */
it("previews current draft and scope without translating user model", async () => {
  vi.mocked(apiClient.post).mockResolvedValue(result);
  render(<RoutePreview {...props} />);
  fireEvent.click(await selectModel());
  await screen.findByText("No matching deployments");
  expect(apiClient.post).toHaveBeenCalledWith("/route_template/preview", {
    accessToken: "test",
    body: {
      model_name: "用户模型",
      endpoint_id: "chat",
      body: props.body,
      template_id: "template",
      organization_id: "org",
      team_id: "team",
    },
  });
});
/** 草稿修改使旧响应作废，不自动发送新请求；非法草稿禁用按钮，重新合法后可手动重试。 */
it("ignores an in-flight stale response and disables invalid drafts", async () => {
  let resolve!: (value: unknown) => void;
  vi.mocked(apiClient.post).mockReturnValue(
    new Promise((r) => {
      resolve = r;
    }),
  );
  const view = render(<RoutePreview {...props} />);
  fireEvent.click(await selectModel());
  view.rerender(<RoutePreview {...props} body={null} />);
  expect(screen.getByRole("button", { name: "Preview draft route" })).toBeDisabled();
  await act(async () => {
    resolve(result);
  });
  expect(screen.queryByText("No matching deployments")).not.toBeInTheDocument();
  view.rerender(<RoutePreview {...props} body={{ model_routes: [], updated: true }} />);
  expect(screen.getByRole("button", { name: "Preview draft route" })).toBeEnabled();
  expect(apiClient.post).toHaveBeenCalledTimes(1);
});
/** 请求失败显示本地化错误；切换语言保留用户模型并重新翻译状态，无真实数据。 */
it("localizes failure and retains user model across language change", async () => {
  vi.mocked(apiClient.post).mockRejectedValue(new Error("denied"));
  const view = render(<RoutePreview {...props} />);
  fireEvent.click(await selectModel());
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Preview failed"));
  setActiveLocale("zh-CN");
  view.rerender(<RoutePreview {...props} />);
  expect(screen.getByRole("alert")).toHaveTextContent("预览失败");
  expect(screen.getByLabelText("预览公开模型")).toHaveValue("用户模型");
});
/** 目录加载失败时不给出可点击的无效预览，展示明确错误；无外部数据需清理。 */
it("reports endpoint catalog failure", async () => {
  vi.mocked(apiClient.get).mockRejectedValue(new Error("offline"));
  render(<RoutePreview {...props} />);
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Unable to load"));
  expect(screen.getByRole("button", { name: "Preview draft route" })).toBeDisabled();
});

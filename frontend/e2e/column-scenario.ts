import { expect, test, type Page } from "@playwright/test";
import { loginAdmin, stableGoto, watchGateway } from "./helpers";
const valueVerbs = new Set(["创建", "更新", "保存", "上传", "转换"]);
const checkVerbs = new Set(["测试", "测试连接", "清空"]);
/** 用途：操作真实通用页面；参数为页面、路由、唯一名称和本场景操作；返回无，验证列表和结果，隔离 schema 清理记录。 */
async function writeColumn(page: Page, route: string, name: string, verbs: string[]) {
  await stableGoto(page, route);
  if (page.url().includes("/login")) { await loginAdmin(page); await stableGoto(page, route); }
  await expect(page.getByRole("heading").first()).toBeVisible({ timeout: 15_000 });
  const input = page.getByLabel("名称");
  const list = page.getByTestId("column-list");
  const result = page.getByTestId("column-result");
  await input.fill(name);
  let listed = name;
  for (const verb of verbs) {
    let expected = listed;
    if (verb !== "创建" && verb !== "删除" && verb !== "测试" && verb !== "测试连接" && verb !== "清空") {
      expected = `${name}-${verb}`;
      await input.fill(expected);
      await expect(list, `${route} ${verb} before`).not.toContainText(expected);
    }
    const beforeDelete = await list.innerText();
    if (verb === "删除") expect(beforeDelete, `${route} delete before`).toContain(listed);
    await page.getByRole("button", { name: verb, exact: true }).click();
    if (valueVerbs.has(verb)) { await expect(list, `${route} ${verb}`).toContainText(expected, { timeout: 15_000 }); listed = expected; }
    else if (verb === "删除") await expect(list, `${route} ${verb}`).not.toContainText(listed, { timeout: 15_000 });
    else if (checkVerbs.has(verb)) await expect(result, `${route} ${verb}`).toContainText(/succeeded|flushed/, { timeout: 15_000 });
  }
}
/** 注册一个页面的一个业务情况；参数为路由、操作和必要创建步骤，无返回。
 * 每个测试独立登录和创建前置对象，后续情况不依赖其他测试；finally 删除支持删除的自建对象，独占 schema 清理剩余配置。
 */
export function registerColumnScenario(route: string, verb: string, prerequisite: boolean) {
 test("通用页面 " + route + " " + verb, async ({page}) => {
  const guard = watchGateway(page);
  await loginAdmin(page);
  const name = "e2e-column-" + Date.now() + "-" + Math.random().toString(36).slice(2, 8);
  try {
   if (route === "/tag-management") {
    await stableGoto(page, route);
    await expect(page.getByText("当前权限模型不支持标签管理。请通过组织、团队和项目管理访问范围。")).toBeVisible();
    await expect(page.getByRole("button", {name:"创建",exact:true})).toHaveCount(0);
   } else await writeColumn(page, route, name, prerequisite ? ["创建", verb] : [verb]);
   await guard.assertOk();
  } finally {
   if ((route === "/search-tools" || route === "/prompts") && verb !== "删除") {
    // 页面保存创建结果 ID，清理也从同一页面发出真实删除，避免遗留前置数据。
    await page.getByRole("button",{name:"删除",exact:true}).click();
    await expect(page.getByTestId("column-list")).not.toContainText(name);
   }
  }
 });
}

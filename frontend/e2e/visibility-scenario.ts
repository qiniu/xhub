import { requireAuthorizationError } from "./assertions";
import { GATEWAY, UPSTREAM } from "./helpers";
import { expect, test, type Page } from "@playwright/test";
import { login, loginAdmin, stableGoto, t, watchGateway } from "./helpers";

/** 权限链通过真实控制台登录构造四层角色；后台结果与页面可见范围必须一致。
 * 每个角色/写权限情况独立注册测试，拒绝同时核验错误契约、持久化快照和上游计数。 */

const GW = GATEWAY;
const PASSWORD: string = "chain-password-1";

let seq = 0;
/** 生成测试资源唯一名称；参数为业务后缀，返回时间与序号组成的名称；场景建数据调用，无持久化。 */
const uniq = (name: string) => `e2e-chain-${Date.now()}-${++seq}-${name}`;

/** 提取控制台真实会话；参数为已登录页面，返回会话 key；缺 cookie 抛错，不修改状态。 */
async function sessionFrom(page: Page): Promise<string> {
  const cookies = await page.context().cookies();
  const token = cookies.find((c) => c.name === "token")?.value;
  expect(token, "the console token cookie is missing").toBeTruthy();
  return JSON.parse(Buffer.from(token!.split(".")[1], "base64url").toString()).key as string;
}

/** 创建绑定会话的接口调用方；参数为页面、操作者会话、管理员观察会话，返回调用方法。
 * 权限场景调用；观察会话只读取写入前后快照，拒绝函数会抛错，成功写请求按传入数据修改测试租户。 */
function as(page: Page, session: string, observerSession = session) {
  /** 发送真实网关请求；参数为方法、路径和可选 JSON，返回响应；所有写入限于自建租户。 */
  const send = (method: string, path: string, data?: unknown) =>
    page.request.fetch(`${GW}${path}`, {
      method,
      headers: { Authorization: `Bearer ${session}`, "Content-Type": "application/json" },
      data: data === undefined ? undefined : JSON.stringify(data),
    });

  return {
    session,
    /** 发送必须成功的请求；参数为方法、路径、JSON，返回解析结果；错误抛出含路径的断言。 */
    async ok(method: "GET" | "POST" | "PATCH", path: string, data?: unknown) {
      const res = await send(method, path, data);
      const text = await res.text();
      expect(res.ok(), `${method} ${path}: ${res.status()} ${text}`).toBeTruthy();
      return JSON.parse(text);
    },
    /** 用途：验证明确的鉴权状态与错误类型；参数为方法、路径、数据和预期状态，返回 true。
     * 越权场景调用，写请求前后读取管理员快照并核对上游计数，异常抛错，不接受 500 作为拒绝。 */
    async denied(method: "GET" | "POST", path: string, data?: unknown, expected: 401 | 403 | 404 = path.startsWith("/team/info?") || path.startsWith("/user/info?") ? 404 : 403) {
      const payload = data as {team_id?: string; user_id?: string} | undefined;
      const readPath = path === "/team/update" ? "/team/info?team_id=" + payload?.team_id : path === "/user/set_password" ? "/user/info?user_id=" + payload?.user_id : "/user/list";
      const observerHeaders = {Authorization: "Bearer " + observerSession};
      /** 读取管理员快照；无参数，返回 JSON；读失败直接失败，禁止比较错误体而伪通过。 */
      const snapshot = async () => { const response = await page.request.get(GW + readPath, {headers: observerHeaders}); expect(response.status(), await response.text()).toBe(200); return response.json(); };
      const before = method === "POST" ? await snapshot() : undefined;
      /** 读取上游请求计数；返回计数快照，计数接口失败直接失败；用于证明越权请求未转发。 */
      const upstreamCount = async () => {const response = await page.request.get(UPSTREAM + "/e2e/request-count");expect(response.status()).toBe(200);return response.json();};
      const upstreamBefore = await upstreamCount();
      const res = await send(method, path, data);
      requireAuthorizationError(res.status(), await res.json(), expected);
      if (method === "POST") expect(await snapshot(), "越权请求不能改变数据").toEqual(before);
      expect(await upstreamCount(), "越权请求不能调用上游").toEqual(upstreamBefore);
      return true;
    },
    /** 读取列表原始响应；参数 GET 与路径，返回真实响应；无持久化副作用。 */
    raw: (method: "GET", path: string) => send(method, path),
  };
}

/** 使用控制台表单登录自建账号；参数为页面与邮箱，返回邮箱和会话；验证导航可见并更新浏览器 cookie。 */
async function signInAs(page: Page, email: string) {
  await login(page, email, PASSWORD);
  await expect(page.getByText(t("nav.apiKeys")).first()).toBeVisible({ timeout: 20_000 });
  return { email, session: await sessionFrom(page) };
}



/** 用途：注册一个角色的真实浏览器权限场景；参数为角色，返回无。前置独立租户与真实会话；验证角色矩阵、错误契约与拒绝无副作用，finally 删除自建项目、用户、团队和组织。 */
export function registerVisibilityScenario(role: "platform" | "organization" | "team" | "member" | "password", situation = "visibility") {
 test("权限链独立场景 " + role + " " + situation, async ({page}) => {
  test.setTimeout(240_000);
  const guard = watchGateway(page);

  // ---------- the platform administrator builds the tenant ----------
  await loginAdmin(page);
  const admin = as(page, await sessionFrom(page));

  const orgA = await admin.ok("POST", "/organization/new", { organization_alias: uniq("orgA") });
  const orgB = await admin.ok("POST", "/organization/new", { organization_alias: uniq("orgB") });

  const teamA1 = await admin.ok("POST", "/team/new", {
    organization_id: orgA.organization_id,
    team_alias: uniq("teamA1"),
  });
  const teamA2 = await admin.ok("POST", "/team/new", {
    organization_id: orgA.organization_id,
    team_alias: uniq("teamA2"),
  });
  const teamB1 = await admin.ok("POST", "/team/new", {
    organization_id: orgB.organization_id,
    team_alias: uniq("teamB1"),
  });

  const orgAdminAEmail = `${uniq("orgadminA")}@example.com`;
  const teamAdminA1Email = `${uniq("teamadminA1")}@example.com`;
  const memberA1Email = `${uniq("memberA1")}@example.com`;
  const orgAdminBEmail = `${uniq("orgadminB")}@example.com`;
  const memberB1Email = `${uniq("memberB1")}@example.com`;

  // Every account is created with its team, its team role and its organization
  // in one request. That is the shape the console now sends: the membership is
  // no longer a second call that could fail and leave a person behind.
  /** 创建独立测试账号；参数邮箱与组织/团队配额，返回真实用户；场景 finally 删除。 */
  const newUser = (email: string, extra: Record<string, unknown>) => {
    const account = { user_email: email, password: PASSWORD, user_role: "user", ...extra };
    return admin.ok("POST", "/user/new", account);
  };

  // The organization administrator is deliberately a plain member of a team in
  // their own organization: their reach comes from the organization, not from a
  // team role, and the two must not be conflated.
  const orgAdminAScope = {
    team_id: teamA1.team_id,
    team_role: "user",
    organization_id: orgA.organization_id,
    max_budget: 12.5,
  };
  const orgAdminA = await newUser(orgAdminAEmail, orgAdminAScope);
  const teamAdminA1 = await newUser(teamAdminA1Email, { team_id: teamA1.team_id, team_role: "admin" });
  const memberA1 = await newUser(memberA1Email, { team_id: teamA1.team_id, team_role: "user" });
  // The account that runs org B, which the org A administrator must not reach.
  const orgAdminB = await newUser(orgAdminBEmail, {
    team_id: teamB1.team_id,
    team_role: "user",
    organization_id: orgB.organization_id,
  });
  const memberB1 = await newUser(memberB1Email, { team_id: teamB1.team_id, team_role: "user" });

  // The budget the create named is stored, not accepted and dropped.
  expect(orgAdminA.max_budget, "the create must store the budget it was given").toBe(12.5);

  const projectA1 = await admin.ok("POST", "/project/new", {
    team_id: teamA1.team_id,
    project_alias: uniq("projectA1"),
  });

  const tenant = {
    orgA: orgA.organization_id as string,
    orgB: orgB.organization_id as string,
    teamA1: teamA1.team_id as string,
    teamA2: teamA2.team_id as string,
    teamB1: teamB1.team_id as string,
    projectA1: projectA1.project_id as string,
    orgAdminAId: orgAdminA.user_id as string,
    teamAdminA1Id: teamAdminA1.user_id as string,
    memberA1Id: memberA1.user_id as string,
    orgAdminBId: orgAdminB.user_id as string,
    memberB1Id: memberB1.user_id as string,
  };

  /** 读取当前角色可见资源 ID；参数为会话调用方，返回 ID 数组；仅查询无副作用。 */
  const teamIDs = async (caller: ReturnType<typeof as>) => {
    const body = await caller.ok("GET", "/v2/team/list?page=1&page_size=500");
    return (body.teams as Array<{ team_id: string }>).map((x) => x.team_id);
  };
  /** 读取当前角色可见资源 ID；参数为会话调用方，返回 ID 数组；仅查询无副作用。 */
  const userIDs = async (caller: ReturnType<typeof as>) => {
    const body = await caller.ok("GET", "/user/list");
    return (body.users as Array<{ user_id: string }>).map((x) => x.user_id);
  };
  /** 读取当前角色可见资源 ID；参数为会话调用方，返回 ID 数组；仅查询无副作用。 */
  const projectIDs = async (caller: ReturnType<typeof as>) => {
    const body = await caller.ok("GET", "/project/list");
    return (body.projects as Array<{ project_id: string }>).map((x) => x.project_id);
  };

  try {
  if (role === "platform") {
  // ---------- the platform administrator sees everything ----------
  const adminTeams = await teamIDs(admin);
  for (const id of [tenant.teamA1, tenant.teamA2, tenant.teamB1]) {
    expect(adminTeams, "the platform administrator must see every team").toContain(id);
  }

  const adminUsers = await userIDs(admin);
  for (const id of [tenant.orgAdminAId, tenant.teamAdminA1Id, tenant.memberA1Id, tenant.orgAdminBId, tenant.memberB1Id]) {
    expect(adminUsers, "the platform administrator must see every account").toContain(id);
  }

  expect(await projectIDs(admin), "the platform administrator must see every project").toContain(tenant.projectA1);

  // The organization listing is a bare array rather than a wrapped collection.
  const adminOrgs = (await (await admin.raw("GET", "/organization/list")).json()) as Array<{ organization_id: string }>;
  const adminOrgIDs = adminOrgs.map((o) => o.organization_id);
  expect(adminOrgIDs).toContain(tenant.orgA);
  expect(adminOrgIDs).toContain(tenant.orgB);


  }
  if (role === "organization") {
  // ---------- the organization administrator of A ----------
  const orgAdmin = as(page, (await signInAs(page, orgAdminAEmail)).session, admin.session);

  if (situation === "visibility") {
  const orgAdminTeams = await teamIDs(orgAdmin);
  expect(orgAdminTeams, "an org admin must see their own team").toContain(tenant.teamA1);
  expect(orgAdminTeams, "an org admin must see every team of their organization").toContain(tenant.teamA2);
  expect(orgAdminTeams, "an org admin must not see another organization").not.toContain(tenant.teamB1);

  expect(await projectIDs(orgAdmin), "an org admin must see their organization's projects").toContain(
    tenant.projectA1,
  );

  const orgAdminUsers = await userIDs(orgAdmin);
  expect(orgAdminUsers, "an org admin must see the people of their organization").toContain(tenant.memberA1Id);
  expect(orgAdminUsers, "an org admin must not see another organization").not.toContain(tenant.memberB1Id);

  expect(
    await orgAdmin.denied("GET", `/team/info?team_id=${tenant.teamB1}`),
    "an org admin must not read another organization's team",
  ).toBe(true);

  }
  if (situation === "allocation") {
  // 组织管理员从页面分配三项额度；服务端读取确认落库，模型和封禁状态仍归平台管理。
  await stableGoto(page, "/teams?team=" + tenant.teamA1);
  await page.getByRole("tab", { name: t("Settings"), exact: true }).click();
  await page.getByRole("button", { name: t("Edit Settings"), exact: true }).click();
  await page.getByLabel(t("Max Budget (USD)"), { exact: true }).fill("30");
  await page.getByLabel(t("Requests per minute Limit (RPM)"), { exact: true }).fill("10");
  await page.getByLabel(t("Tokens per minute Limit (TPM)"), { exact: true }).fill("1000");
  const savedLimits = page.waitForResponse(
    response => response.url().endsWith("/team/update") && response.request().method() === "POST",
  );
  await page.getByRole("button", { name: t("Save Changes"), exact: true }).click();
  expect((await savedLimits).status(), "组织管理员应能保存本组织团队额度").toBe(200);
  const ownTeam = await orgAdmin.ok("GET", `/team/info?team_id=${tenant.teamA1}`);
  expect(ownTeam.team_info).toMatchObject({ max_budget: 30, rpm_limit: 10, tpm_limit: 1000 });
  }
  const prohibited: Record<string, Record<string, unknown>> = {
    "foreign-budget": { max_budget: 30 }, "foreign-rpm": { rpm_limit: 10 }, "foreign-tpm": { tpm_limit: 1000 },
    "models": { models: ["gpt-4o-mini"] }, "status": { status: "blocked" },
  };
  if (prohibited[situation]) await orgAdmin.denied("POST", "/team/update", {team_id: situation.startsWith("foreign-") ? tenant.teamB1 : tenant.teamA1, ...prohibited[situation]});



  }
  if (role === "team") {
  // ---------- the team administrator of team A1 ----------
  const teamAdmin = as(page, (await signInAs(page, teamAdminA1Email)).session, admin.session);

  if (situation === "visibility") {
  const teamAdminTeams = await teamIDs(teamAdmin);
  expect(teamAdminTeams, "a team admin must see their own team").toContain(tenant.teamA1);
  expect(teamAdminTeams, "a team admin must not see a sibling team").not.toContain(tenant.teamA2);
  expect(teamAdminTeams, "a team admin must not see another organization").not.toContain(tenant.teamB1);

  }
  const prohibited: Record<string, Record<string, unknown>> = {budget: {max_budget: 1}, rpm: {rpm_limit: 11}, tpm: {tpm_limit: 1001}, models: {models: ["gpt-4o-mini"]}};
  if (prohibited[situation]) await teamAdmin.denied("POST", "/team/update", {team_id: tenant.teamA1, ...prohibited[situation]});



  }
  if (role === "member") {
  // ---------- the plain member ----------
  const member = as(page, (await signInAs(page, memberA1Email)).session, admin.session);

  if (situation === "visibility") {
  const memberTeams = await teamIDs(member);
  expect(memberTeams, "a member must see the team they belong to").toContain(tenant.teamA1);
  expect(memberTeams, "a member must not see any other team").not.toContain(tenant.teamA2);
  expect(memberTeams, "a member must not see another organization").not.toContain(tenant.teamB1);

  }
  const deniedReads: Record<string, string> = {"other-user": "/user/info?user_id=" + tenant.teamAdminA1Id, usage: "/global/activity", picker: "/user/filter/ui", audit: "/audit/logs"};
  if (deniedReads[situation]) await member.denied("GET", deniedReads[situation]);
  if (situation === "create") await member.denied("POST", "/user/new", {user_email: uniq("smuggled") + "@example.com", password: PASSWORD});
  if (situation === "page") {
  // ---------- the console hides what the server refuses ----------
  // The server being right is not enough: a page that renders rows it should not
  // have is a leak even when every individual call was authorized.
  await stableGoto(page, "/users");
  await expect(page.getByTestId("admin-shell")).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(t("pages.users.noUsersFound")).or(page.getByRole("row")).first()).toBeVisible({
    timeout: 20_000,
  });
  const rendered = await page.getByRole("row").allInnerTexts();
  expect(
    rendered.some((row) => row.includes(teamAdminA1Email) || row.includes(orgAdminBEmail)),
    `a member's account page rendered other people: ${rendered.join(" | ")}`,
  ).toBe(false);
  }


  }
  if (role === "password") {
  const teamAdmin = as(page, (await signInAs(page, teamAdminA1Email)).session, admin.session);
  const member = as(page, (await signInAs(page, memberA1Email)).session, admin.session);
  if (situation === "reset") {
  // ---------- the password reset follows the chain ----------
  // A team administrator hands their own member a new password. This is the
  // write that replaced the reset link, which pointed at an onboarding page
  // whose claim endpoint discarded the password.
  const newPassword = "chain-replaced-password-1";
  const reset = await teamAdmin.ok("POST", "/user/set_password", {
    user_id: tenant.memberA1Id,
    password: newPassword,
  });
  expect(reset.password_updated, "the reset must report that it applied").toBe(true);

  // It takes effect immediately: the member signs in with the new password, and
  // the session they had before it no longer works.
  const staleSession = member;
  await login(page, memberA1Email, newPassword);
  await expect(page.getByText(t("nav.apiKeys")).first()).toBeVisible({ timeout: 20_000 });
  expect(
    await staleSession.denied("GET", "/user/info", undefined, 401),
    "the session issued before the reset must stop working",
  ).toBe(true);

  }
  if (situation === "deny-organization" || situation === "deny-foreign") {
    const user_id = situation === "deny-organization" ? tenant.orgAdminAId : tenant.memberB1Id;
    // 跨组织目标按鉴权契约隐藏为 404，同组织高权限账户明确返回 403。
    await teamAdmin.denied("POST", "/user/set_password", {user_id, password: "chain-should-not-apply-1"}, situation === "deny-foreign" ? 404 : 403);
    // 密码不在用户信息快照中；通过原密码真实登录证明拒绝没有改动密码。
    await signInAs(page, situation === "deny-organization" ? orgAdminAEmail : memberB1Email);
  }
  if (situation === "admin-reset") {
  // ---------- the platform administrator can reset anybody ----------
  const adminReset = await admin.ok("POST", "/user/set_password", {
    user_id: tenant.orgAdminAId,
    password: "chain-admin-reset-1",
  });
  expect(adminReset.password_updated).toBe(true);
  await login(page, orgAdminAEmail, "chain-admin-reset-1");
  await expect(page.getByText(t("nav.apiKeys")).first()).toBeVisible({ timeout: 20_000 });
  }


  }
  await guard.assertOk();
  } finally {
    test.setTimeout(test.info().timeout + 30_000);
    await admin.ok("POST", "/project/delete", {project_id: tenant.projectA1});
    // 先删除测试团队以解除最后一位管理员约束，再删除账户；不能把清理错误遮蔽成业务失败。
    for (const team_id of [tenant.teamA1, tenant.teamA2, tenant.teamB1]) await admin.ok("POST", "/team/delete", {team_id});
    for (const user_id of [tenant.orgAdminAId, tenant.teamAdminA1Id, tenant.memberA1Id, tenant.orgAdminBId, tenant.memberB1Id]) await admin.ok("POST", "/user/delete", {user_id});
    for (const organization_id of [tenant.orgA, tenant.orgB]) {
      const removed = await page.request.delete(GW + "/organization/delete", {headers: {Authorization: "Bearer " + admin.session}, data: {organization_id}});
      expect(removed.status(), await removed.text()).toBe(200);
    }
  }
 });
}

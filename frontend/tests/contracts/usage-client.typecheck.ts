import { teamDailyActivityAggregatedCall } from "../../src/components/networking";
type TeamResult = Awaited<ReturnType<typeof teamDailyActivityAggregatedCall>>;
// 默认any会让这条预期错误失效，从而阻止遗漏生成类型的客户端通过编译。
// @ts-expect-error 团队日报必须是生成结构，不能接受字符串或any
const invalidTeamResult: TeamResult = "not a daily usage response";
void invalidTeamResult;

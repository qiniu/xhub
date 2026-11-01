import { queryOptions, useQuery } from "@tanstack/react-query";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { uiSpendLogDetailsCall } from "@/components/networking";

/** 构建共享详情查询；参数为当前凭据与请求 ID，返回含缓存键、读取函数和有效期的查询选项。
 * 深链接与抽屉共用，复用同一请求中的完整正文，避免重复传输及重复读审计；不同凭据始终隔离。缺少输入时不查询。
 */
export const logDetailsQueryOptions = (accessToken: string | null, requestId: string | undefined) => queryOptions({
  queryKey: ["logDetails", requestId, accessToken],
  queryFn: async () => {
    if (!accessToken || !requestId) return null;
    return await uiSpendLogDetailsCall(accessToken, requestId, "");
  },
  enabled: !!accessToken && !!requestId,
  staleTime: 10 * 60 * 1000,
  gcTime: 10 * 60 * 1000,
});

/** 按请求 ID 懒加载授权详情；参数为请求 ID、兼容旧调用的列表起点和启用标志，返回查询状态。
 * 供日志抽屉调用，不继承列表日期；与深链接共用查询，失败保留错误状态供重试，不伪装为空记录。
 */
export const useLogDetails = (requestId: string | undefined, _startTime: string | undefined, enabled: boolean) => {
  const { accessToken } = useAuthorized();
  return useQuery({
    ...logDetailsQueryOptions(accessToken, requestId),
    enabled: enabled && !!accessToken && !!requestId,
  });
};

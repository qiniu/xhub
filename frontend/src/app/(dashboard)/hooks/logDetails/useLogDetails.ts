import { useQuery } from "@tanstack/react-query";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { uiSpendLogDetailsCall } from "@/components/networking";

/** 按请求 ID 懒加载授权详情；参数为请求 ID、兼容旧调用的列表起点和启用标志，返回查询状态。
 * 供日志抽屉调用。详情不受列表时间范围限制，缓存按登录凭据隔离；失败由查询错误交给界面，不伪装为空记录。
 */
export const useLogDetails = (requestId: string | undefined, _startTime: string | undefined, enabled: boolean) => {
  const { accessToken } = useAuthorized();
  return useQuery({
    queryKey: ["logDetails", requestId, accessToken],
    queryFn: async () => {
      if (!accessToken || !requestId) return null;
      return await uiSpendLogDetailsCall(accessToken, requestId, "");
    },
    enabled: enabled && !!accessToken && !!requestId,
    staleTime: 10 * 60 * 1000,
    gcTime: 10 * 60 * 1000,
  });
};

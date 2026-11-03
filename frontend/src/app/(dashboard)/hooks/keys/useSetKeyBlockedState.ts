import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/components/networking";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { keyKeys } from "./useKeys";
import { useT } from "@/i18n";
import { t } from "@/i18n/runtime";

/** 禁用/恢复请求；keyToken为已有记录标识，blocked为目标状态，不修改密钥归属或额度。 */
export interface SetKeyBlockedStateInput {
  keyToken: string;
  blocked: boolean;
}

/** 后台明确确认后的状态；仅通过响应校验后返回，供详情页更新显示。 */
export interface SetKeyBlockedStateResult {
  blocked: boolean;
}

/** 提交Key状态操作并核对确认；输入会话与目标状态，返回后台确认值。
 * 供详情mutation调用；HTTP失败原样抛出，空/错误/相反状态抛出当前语言提示，不推测写入结果或重试。 */
export const setKeyBlockedState = async (
  accessToken: string,
  { keyToken, blocked }: SetKeyBlockedStateInput,
): Promise<SetKeyBlockedStateResult> => {
  const response = await apiClient.post<unknown>(blocked ? "/key/block" : "/key/unblock", {
    accessToken,
    body: { key: keyToken },
  });
  // HTTP 200不证明写入，响应可能在中间层丢失；不能用请求目标补成成功，也不能反向宣称后台没有写入。
  if (!response || typeof response !== "object" || Array.isArray(response) || !("blocked" in response) || response.blocked !== blocked) {
    throw new Error(t("keyActions.invalidStateResponse"));
  }
  return { blocked: response.blocked };
};

/** 为详情页提供状态mutation；使用当前会话，仅明确成功时刷新Key缓存，失败交由页面展示，无自动重试。 */
export const useSetKeyBlockedState = () => {
  const t = useT();
  const { accessToken } = useAuthorized();
  const queryClient = useQueryClient();

  return useMutation<SetKeyBlockedStateResult, Error, SetKeyBlockedStateInput>({
    mutationFn: async (input) => {
      if (!accessToken) {
        throw new Error(t("Access token is required"));
      }
      return setKeyBlockedState(accessToken, input);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: keyKeys.all });
    },
  });
};

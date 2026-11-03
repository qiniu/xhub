/** API sort field mapping for /spend/logs/ui endpoint */
export const LOGS_SORT_FIELD_MAP = {
  startTime: "startTime",
  spend: "spend",
  total_tokens: "total_tokens",
  request_duration_ms: "request_duration_ms",
  model: "model",
  ttft_ms: "ttft_ms",
} as const;

export type LogsSortField = keyof typeof LOGS_SORT_FIELD_MAP;

export type LogEntry = {
  request_id: string;
  litellm_call_id?: string | null;
  api_key: string;
  team_id: string;
  model: string;
  model_id: string;
  model_group?: string;
  api_base?: string;
  call_type: string;
  spend: number;
  total_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  startTime: string;
  endTime: string;
  user?: string;
  end_user?: string;
  custom_llm_provider?: string;
  /** 调用时持久化的模型提供商凭证名称；缺失时不以协议名称代替。 */
  credential_name?: string;
  /** 客户端提供的通用备注，可关联验收步骤，不代表测试结果。 */
  label?: string;
  /** 客户端明确指定的推理强度；旧记录或未指定时为空，不推断模型默认值。 */
  reasoning_effort?: string;
  metadata?: Record<string, any>;
  cache_hit: string | boolean;
  cache_key?: string;
  request_tags?: Record<string, any>;
  requester_ip_address?: string;
  /** 存储正文保持任意JSON；消费者展示前自行判断形状，不假定只会收到消息数组。 */
  messages: unknown;
  response: unknown;
  proxy_server_request?: unknown;
  session_id?: string;
  /** 后端 PlanRoute 识别会话的来源：explicit（标准 ID）、parent（父响应）、
   * cache_key（缓存键哈希）、prompt（稳定首轮指纹）、generated（随机 UUID）。
   * 日志表仅对 cache_key/prompt 显示推断标记；空值表示没有来源事实，不代表供应商。 */
  session_source?: string;
  status?: string;
  /** 完整错误正文；即使关闭提示词存储也可用于请求失败诊断。 */
  error?: string | null;
  /** 调用时保存的端点与首字节计时，TPS 由后台统一计算。 */
  inbound_endpoint?: string;
  outbound_endpoint?: string;
  streamed?: boolean;
  ttft_ms?: number | null;
  tps?: number | null;
  completionStartTime?: string;
  request_duration_ms?: number;
  session_total_count?: number;
  session_total_spend?: number;
  session_total_tokens?: number;
  session_total_prompt_tokens?: number;
  session_total_completion_tokens?: number;
  session_cache_hit_count?: number;
  mcp_tool_call_count?: number;
  mcp_tool_call_spend?: number;
  session_llm_count?: number;
  session_agent_count?: number;
  session_models?: string[];
  session_models_truncated?: boolean;
};

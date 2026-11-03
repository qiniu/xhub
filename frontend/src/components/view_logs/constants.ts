export const ERROR_CODE_OPTIONS: { label: string; value: string }[] = [
  { label: "logsUX.code400", value: "400" },
  { label: "logsUX.code401", value: "401" },
  { label: "logsUX.code403", value: "403" },
  { label: "logsUX.code404", value: "404" },
  { label: "logsUX.code408", value: "408" },
  { label: "logsUX.code413", value: "413" },
  { label: "logsUX.code422", value: "422" },
  { label: "logsUX.code499", value: "499" },
  { label: "logsUX.code429", value: "429" },
  { label: "logsUX.code500", value: "500" },
  { label: "logsUX.code502", value: "502" },
  { label: "logsUX.code503", value: "503" },
  { label: "logsUX.code529", value: "529" },
];

/** Call types that represent MCP tool invocations (shared across columns, index, drawer). */
export const MCP_CALL_TYPES = ["call_mcp_tool", "list_mcp_tools"];

/** Call types that represent agent/A2A requests (e.g. asend_message). */
export const AGENT_CALL_TYPES = ["asend_message"];

/** Call types that represent Batch API operations (creation and retrieval, sync and async). */
export const BATCH_CALL_TYPES = ["acreate_batch", "create_batch", "aretrieve_batch", "retrieve_batch"];

export const QUICK_SELECT_OPTIONS: { label: string; value: number; unit: string }[] = [
  { label: "logsUX.lastMinute", value: 1, unit: "minutes" },
  { label: "logsUX.last15Minutes", value: 15, unit: "minutes" },
  { label: "logsUX.lastHour", value: 1, unit: "hours" },
  { label: "logsUX.last4Hours", value: 4, unit: "hours" },
  { label: "logsUX.last24Hours", value: 24, unit: "hours" },
  { label: "logsUX.last7Days", value: 7, unit: "days" },
  { label: "logsUX.last14Days", value: 14, unit: "days" },
];

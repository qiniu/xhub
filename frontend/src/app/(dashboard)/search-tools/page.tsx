"use client";

import { ProbeCatalog } from "@/components/probe_catalog/ProbeCatalog";

/** 搜索工具管理入口；配置真实供应商后执行网络探测，失败信息在表单中明确显示。 */
export default function Page() {
  return <ProbeCatalog kind="search_tools" />;
}

"use client";

import { ProbeCatalog } from "@/components/probe_catalog/ProbeCatalog";

/** 提示词管理入口；渲染真实保存与模型测试流程，使用登录会话，不执行静态成功占位。 */
export default function Page() {
  return <ProbeCatalog kind="prompts" />;
}

"use client";

import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { CacheManagement } from "@/components/CacheManagement";

/** 缓存页面使用当前会话展示真实运行状态；无参数，返回管理视图；权限仍由既有后台校验。 */
export default function Page() {
  const { accessToken } = useAuthorized();
  return <CacheManagement accessToken={accessToken} />;
}

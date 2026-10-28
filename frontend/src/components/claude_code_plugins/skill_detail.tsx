import React, { useState } from "react";
import { Button } from "@/components/ui/button";
import { Plugin } from "./types";
import { useT } from "@/i18n";
import { getSourceLink } from "./helpers";
import SkillSupportNotice from "./SkillSupportNotice";

/** 兼容旧详情调用方；skill为配置元数据，onBack回到目录，其他属性不授予安装或发布能力。 */
interface SkillDetailProps {
  skill: Plugin;
  onBack: () => void;
  isAdmin?: boolean;
  accessToken?: string | null;
  onPublishClick?: () => void;
}

/** 展示Skill原始元数据与使用边界；skill内容保持原文，onBack只做页面导航。
 * enabled只显示配置值，不代表发布状态；使用页不生成未实现marketplace的安装指令或发送外部请求。
 */
const SkillDetail: React.FC<SkillDetailProps> = ({ skill, onBack }) => {
  const t = useT();
  const [usage, setUsage] = useState(false);
  const sourceUrl = skill.source ? getSourceLink(skill.source) : null;
  const rows = [
    { label: t("Skill ID"), value: skill.id },
    { label: t("Category"), value: skill.category },
    { label: t("Domain"), value: skill.domain },
    { label: t("Namespace"), value: skill.namespace },
    { label: t("Version"), value: skill.version },
    { label: t("Author"), value: skill.author?.name },
    { label: t("Added"), value: skill.created_at },
  ];
  return (
    <div className="space-y-5 py-6">
      <Button variant="ghost" onClick={onBack}>{t("Skills")}</Button>
      <h1 className="text-2xl">{skill.name}</h1>
      {skill.description && <p>{skill.description}</p>}
      <div className="flex gap-2">
        <Button variant={usage ? "outline" : "default"} onClick={() => setUsage(false)}>{t("Overview")}</Button>
        <Button variant={usage ? "default" : "outline"} onClick={() => setUsage(true)}>{t("skillHub.usage")}</Button>
      </div>
      <SkillSupportNotice />
      {!usage && <>
        <p>{t(skill.enabled ? "skillHub.configEnabled" : "skillHub.configDisabled")}</p>
        <table className="w-full text-left text-sm">
          <thead><tr><th>{t("Property")}</th><th>{t("skillHub.value")}</th></tr></thead>
          <tbody>{rows.filter(row => row.value).map(row => <tr key={row.label}><th className="py-2 font-normal">{row.label}</th><td>{row.value}</td></tr>)}</tbody>
        </table>
        <h2>{t("Source")}</h2>
        {sourceUrl && <a href={sourceUrl} target="_blank" rel="noopener noreferrer">{sourceUrl}</a>}
        <pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(skill.source ?? null, null, 2)}</pre>
        {skill.keywords?.length ? <p>{t("Tags")}: {skill.keywords.join(", ")}</p> : null}
      </>}
    </div>
  );
};
export default SkillDetail;

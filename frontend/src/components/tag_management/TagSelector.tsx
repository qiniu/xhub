import React, { useEffect, useState } from "react";
import { Tag } from "./types";
import { tagListCall } from "../networking";
import { MultiSelect } from "@/components/shared/MultiSelect";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { canAccessManagement } from "@/utils/roles";
import { useT } from "@/i18n";

interface TagSelectorProps {
  onChange: (selectedTags: string[]) => void;
  value?: string[];
  className?: string;
  accessToken: string;
}

/** 调试台标签选择器：props保留用户选中值与原文；只为管理身份读取配置。
 * 读取失败显示本地化提示，空配置不报错；卸载或身份改变后旧请求不能覆盖新状态，无预算或权限副作用。 */
const TagSelector: React.FC<TagSelectorProps> = ({ onChange, value, className, accessToken }) => {
  const t = useT();
  const { userRole } = useAuthorized();
  const [tags, setTags] = useState<Tag[]>([]);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let active = true;
    /** 读取当前管理身份的标签；无参数和返回值，失败更新提示，失效请求不写入组件状态。 */
    const fetchTags = async () => {
      if (!accessToken || !canAccessManagement(userRole)) return;
      setLoading(true);
      setFailed(false);
      try {
        const response = await tagListCall(accessToken);
        if (active) setTags(Object.values(response));
      } catch {
        if (active) {
          setTags([]);
          setFailed(true);
        }
      } finally {
        if (active) setLoading(false);
      }
    };
    void fetchTags();
    return () => {
      active = false;
    };
  }, [accessToken, userRole]);

  return (
    <>
      {failed && (
        <p role="alert" className="mb-2 text-sm text-destructive">
          {t("tagSelector.loadFailed")}
        </p>
      )}
      <MultiSelect
        placeholder={t("Select or create tags")}
        onValueChange={onChange}
        value={value}
        loading={loading}
        className={className}
        allowCustomValues
        options={tags.map((tag) => ({
          label: tag.name,
          value: tag.name,
          description: tag.description || undefined,
        }))}
      />
    </>
  );
};

export default TagSelector;

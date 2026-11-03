import React from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Plugin } from "./types";
import { useT } from "@/i18n";
import SkillSupportNotice from "./SkillSupportNotice";

/** 保留旧调用方属性接口；当前仅展示支持状态，不使用凭据、不修改配置、不调用发布成功回调。 */
interface MakeSkillPublicFormProps {
  visible: boolean;
  onClose: () => void;
  accessToken: string;
  skillsList: Plugin[];
  onSuccess: () => void;
}

/** 旧发布入口的支持状态弹窗；visible控制显示，onClose关闭，无提交副作用。
 * enabled只是配置字段，没有发布执行器，不能用无操作或启停请求冒充发布成功。
 */
const MakeSkillPublicForm: React.FC<MakeSkillPublicFormProps> = ({ visible, onClose }) => {
  const t = useT();
  return (
    <Dialog open={visible} onOpenChange={open => { if (!open) onClose(); }} disablePointerDismissal>
      <DialogContent showCloseButton={false} className="sm:max-w-[600px]">
        <DialogHeader><DialogTitle>{t("skillHub.supportAction")}</DialogTitle></DialogHeader>
        <SkillSupportNotice />
        <Button onClick={onClose}>{t("Close")}</Button>
      </DialogContent>
    </Dialog>
  );
};
export default MakeSkillPublicForm;

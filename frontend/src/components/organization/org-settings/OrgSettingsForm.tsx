"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import * as React from "react";

import { organizationKeys } from "@/app/(dashboard)/hooks/organizations/useOrganizations";
import { ModelSelect } from "@/components/ModelSelect/ModelSelect";
import { toast } from "@/lib/toast";
import type { Organization } from "@/components/networking";
import { FieldGroup } from "@/components/ui/field";
import { FormField } from "@/components/shared/form/FormField";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { pickDirty } from "@/lib/forms/pickDirty";
import { useZodForm } from "@/lib/forms/useZodForm";
import { fetchClient } from "@/lib/http/api";

import { buildOrgPatch, orgToForm, type OrgPatchBody } from "./mapper";
import { orgSettingsSchema } from "./schema";
import { useT, t } from "@/i18n";
import RouteTemplateSelect from "@/components/route_templates/RouteTemplateSelect";

export const NO_RESET = "never";

export const BUDGET_DURATION_OPTIONS = [
  { value: NO_RESET, /** 渲染时读取当前语言，避免初始化冻结文案；无写入副作用。 */ get label() { return t("No reset"); } },
  { value: "24h", /** 渲染时读取当前语言，避免初始化冻结文案；无写入副作用。 */ get label() { return t("organizationBudget.every24h"); } },
  { value: "7d", /** 渲染时读取当前语言，避免初始化冻结文案；无写入副作用。 */ get label() { return t("organizationBudget.every7d"); } },
  { value: "30d", /** 渲染时读取当前语言，避免初始化冻结文案；无写入副作用。 */ get label() { return t("organizationBudget.every30d"); } },
] as const;

/** 格式化组织预算周期；value 为服务端周期，返回当前语言文案，详情与选择项共用；未知值不冒充有效周期，无写入副作用。 */
export function organizationBudgetLabel(value: string | null | undefined): string {
  return BUDGET_DURATION_OPTIONS.find(option => option.value === (value || NO_RESET))?.label ?? t("organizationBudget.unknown");
}

/** What the legacy update route actually accepts: the schema's fields plus the id. */
type OrgUpdateBody = OrgPatchBody & { organization_id: string };

/**
 * The generated types declare the legacy PATCH /organization/update operation
 * with no request body, but its handler reads one — that route is how this form
 * has always saved. Only the operation's declaration disagrees, so the body is
 * re-typed here against the schema's own field type and the rest keeps its
 * checking.
 */
const patchOrganization = fetchClient.PATCH as unknown as (
  path: "/organization/update",
  options: { body: OrgUpdateBody },
) => Promise<{ data?: unknown }>;

const defaultPatchOrganization = async (organizationId: string, body: OrgPatchBody): Promise<unknown> => {
  const { data } = await patchOrganization("/organization/update", {
    body: { organization_id: organizationId, ...body },
  });
  return data;
};

interface OrgSettingsFormProps {
  organizationId: string;
  org: Organization;
  accessToken: string;
  canEditModels?: boolean;
  canEditLimits?: boolean;
  onCancel: () => void;
  onSaved: () => void;
  patchOrganization?: (organizationId: string, body: OrgPatchBody) => Promise<unknown>;
}

/** 组织设置表单：接收组织快照、身份及保存/取消回调，返回可编辑表单。
 * canEditModels/canEditLimits 由平台角色决定，组织管理员只读模型、预算周期与速率上限；仅提交实际修改字段，非法输入不提交，保存失败保留输入。 */
export const OrgSettingsForm = ({
  organizationId,
  org,
  accessToken,
  canEditModels = true,
  canEditLimits = canEditModels,
  onCancel,
  onSaved,
  patchOrganization = defaultPatchOrganization,
}: OrgSettingsFormProps) => {
  const t = useT();
  const queryClient = useQueryClient();
  const form = useZodForm(orgSettingsSchema, { defaultValues: orgToForm(org) });
  // RHF 按读取字段订阅状态；渲染时订阅 dirtyFields，避免连续编辑多个字段后只提交部分修改项。
  const { isDirty, dirtyFields } = form.formState;

  const mutation = useMutation({
    mutationFn: (body: OrgPatchBody) => patchOrganization(organizationId, body),
    onSuccess: () => {
      toast.success(t("Organization settings updated successfully"));
      queryClient.invalidateQueries({ queryKey: organizationKeys.all });
      onSaved();
    },
    onError: (error: unknown) =>
      toast.fromError(error instanceof Error ? error.message : t("Failed to update organization settings")),
  });

  // 校验成功后将当前脏字段转换为接口补丁，清空限额转换为 null，未编辑字段保持原值。
  const onSubmit = form.handleSubmit((values) => {
    mutation.mutate(buildOrgPatch(pickDirty(values, dirtyFields)));
  });

  return (
    <form onSubmit={onSubmit} noValidate>
      <FieldGroup>
        <FormField control={form.control} name="organization_alias" label={t("Organization Name")}>
          {({ ref, ...field }) => <Input {...field} ref={ref} />}
        </FormField>

        <FormField control={form.control} name="models" label={t("Models")}>
          {(field) => (
            <ModelSelect
              id={field.id}
              disabled={!canEditModels}
              value={field.value}
              onChange={field.onChange}
              context="organization"
              options={{ includeSpecialOptions: true, showAllProxyModelsOverride: true }}
            />
          )}
        </FormField>

        <p className="text-sm text-muted-foreground">{t(canEditModels ? "organizationConfig.modelScopeHint" : "organizationConfig.modelScopeReadOnly")}</p>

        <FormField control={form.control} name="route_template_id" label={t("pages.routeTemplates.title")}>
          {({ value, onChange }) => (
            <RouteTemplateSelect
              accessToken={accessToken}
              value={value}
              onChange={onChange}
              scope="organization"
              scopeId={organizationId}
            />
          )}
        </FormField>

        <p className="text-sm text-muted-foreground">{t(canEditLimits ? "organizationBudget.periodHint" : "organizationBudget.readOnly")}</p>
        <FormField control={form.control} name="max_budget" label={t("Max Budget (USD)")}>
          {({ ref, ...field }) => <Input {...field} ref={ref} disabled={!canEditLimits} type="number" step="any" min={0} />}
        </FormField>

        <FormField control={form.control} name="budget_duration" label={t("Reset Budget")}>
          {({ id, value, onChange, "aria-invalid": ariaInvalid, "aria-describedby": ariaDescribedBy }) => (
            <Select
              items={BUDGET_DURATION_OPTIONS}
              disabled={!canEditLimits}
              value={value === "" ? NO_RESET : value}
              onValueChange={(selected) => onChange(selected === NO_RESET ? "" : selected)}
            >
              <SelectTrigger id={id} aria-invalid={ariaInvalid} aria-describedby={ariaDescribedBy}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {BUDGET_DURATION_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </FormField>

        <p className="text-sm text-muted-foreground">{t("quotaGuide.rates")}</p>
        <FormField control={form.control} name="tpm_limit" label={t("Tokens per minute Limit (TPM)")}>
          {({ ref, ...field }) => <Input {...field} ref={ref} disabled={!canEditLimits} type="number" step={1} min={0} max={2147483647} placeholder={t("quotaGuide.rateBlank")} />}
        </FormField>

        <FormField control={form.control} name="rpm_limit" label={t("Requests per minute Limit (RPM)")}>
          {({ ref, ...field }) => <Input {...field} ref={ref} disabled={!canEditLimits} type="number" step={1} min={0} max={2147483647} placeholder={t("quotaGuide.rateBlank")} />}
        </FormField>

        <FormField control={form.control} name="metadata" label={t("Metadata")}>
          {({ ref, ...field }) => <Textarea {...field} ref={ref} rows={4} />}
        </FormField>
      </FieldGroup>

      <div className="sticky z-chrome bg-card p-4 border-t border-border -bottom-6 -inset-x-6 mt-6">
        <div className="flex justify-end items-center gap-2">
          <Button type="button" variant="outline" onClick={onCancel} disabled={mutation.isPending}>
            {t("Cancel")}
          </Button>
          <Button type="submit" disabled={!isDirty || mutation.isPending}>
            {mutation.isPending ? t("Saving...") : t("Save Changes")}
          </Button>
        </div>
      </div>
    </form>
  );
};

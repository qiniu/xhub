"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import * as React from "react";

import { organizationKeys } from "@/app/(dashboard)/hooks/organizations/useOrganizations";
import { ModelSelect } from "@/components/ModelSelect/ModelSelect";
import { toast } from "@/lib/toast";
import { FieldGroup } from "@/components/ui/field";
import { FormField } from "@/components/shared/form/FormField";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useZodForm } from "@/lib/forms/useZodForm";
import { fetchClient } from "@/lib/http/api";
import { useT } from "@/i18n";
import RouteTemplateSelect from "@/components/route_templates/RouteTemplateSelect";

import { BUDGET_DURATION_OPTIONS, NO_RESET } from "../org-settings/OrgSettingsForm";
import { orgSettingsSchema } from "../org-settings/schema";
import { buildOrgCreateBody, emptyOrgFormValues, type OrgCreateBody } from "./mapper";

const defaultCreateOrganization = async (body: OrgCreateBody): Promise<unknown> => {
  const { data } = await fetchClient.POST("/organization/new", { body });
  return data;
};

interface OrgCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  accessToken: string;
  createOrganization?: (body: OrgCreateBody) => Promise<unknown>;
}

/** 组织创建弹窗：接收打开状态、会话及可选创建接口，返回表单；校验后提交固定预算周期、模型上限和元数据。
 * 管理页面调用，成功清空并关闭，失败保留用户输入，取消不写数据。 */
export const OrgCreateDialog = ({
  open,
  onOpenChange,
  accessToken,
  createOrganization = defaultCreateOrganization,
}: OrgCreateDialogProps) => {
  const t = useT();
  const queryClient = useQueryClient();
  const form = useZodForm(orgSettingsSchema, { defaultValues: emptyOrgFormValues });

  const closeAndReset = () => {
    form.reset(emptyOrgFormValues);
    onOpenChange(false);
  };

  const mutation = useMutation({
    mutationFn: (body: OrgCreateBody) => createOrganization(body),
    onSuccess: () => {
      toast.success(t("Organization created successfully"));
      queryClient.invalidateQueries({ queryKey: organizationKeys.all });
      closeAndReset();
    },
    onError: (error: unknown) =>
      toast.fromError(error instanceof Error ? error.message : t("Failed to create organization")),
  });

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen && mutation.isPending) return;
    if (!nextOpen) {
      form.reset(emptyOrgFormValues);
    }
    onOpenChange(nextOpen);
  };

  const onSubmit = form.handleSubmit((values) => {
    if (mutation.isPending) return;
    mutation.mutate(buildOrgCreateBody(values));
  });

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-3xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("pages.organizations.createTitle")}</DialogTitle>
        </DialogHeader>

        <form onSubmit={onSubmit} noValidate>
          <FieldGroup>
            <FormField control={form.control} name="organization_alias" label={t("Organization Name")}>
              {({ ref, ...field }) => <Input {...field} ref={ref} />}
            </FormField>

            <FormField control={form.control} name="models" label={t("Models")}>
              {(field) => (
                <ModelSelect
                  id={field.id}
                  value={field.value}
                  onChange={field.onChange}
                  context="organization"
                  options={{ includeSpecialOptions: true, showAllProxyModelsOverride: true }}
                />
              )}
            </FormField>

            <p className="text-sm text-muted-foreground">{t("organizationConfig.modelScopeHint")}</p>

            <FormField control={form.control} name="route_template_id" label={t("pages.routeTemplates.title")}>
              {({ value, onChange }) => (
                <RouteTemplateSelect accessToken={accessToken} value={value} onChange={onChange} scope="organization" />
              )}
            </FormField>

            <FormField control={form.control} name="max_budget" label={t("Max Budget (USD)")}>
              {({ ref, ...field }) => <Input {...field} ref={ref} type="number" step="any" min={0} />}
            </FormField>

            <p className="text-sm text-muted-foreground">{t("organizationBudget.periodHint")}</p>
            <FormField control={form.control} name="budget_duration" label={t("Reset Budget")}>
              {({ id, value, onChange, "aria-invalid": ariaInvalid, "aria-describedby": ariaDescribedBy }) => (
                <Select
                  items={BUDGET_DURATION_OPTIONS}
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
              {({ ref, ...field }) => <Input {...field} ref={ref} type="number" step={1} min={0} max={2147483647} placeholder={t("quotaGuide.rateBlank")} />}
            </FormField>

            <FormField control={form.control} name="rpm_limit" label={t("Requests per minute Limit (RPM)")}>
              {({ ref, ...field }) => <Input {...field} ref={ref} type="number" step={1} min={0} max={2147483647} placeholder={t("quotaGuide.rateBlank")} />}
            </FormField>

            <FormField control={form.control} name="metadata" label={t("Metadata")}>
              {({ ref, ...field }) => <Textarea {...field} ref={ref} rows={4} />}
            </FormField>
          </FieldGroup>

          <DialogFooter className="mt-6">
            <Button
              type="button"
              variant="outline"
              onClick={() => handleOpenChange(false)}
              disabled={mutation.isPending}
            >
              {t("Cancel")}
            </Button>
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending ? t("common.creating") : t("pages.organizations.createTitle")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
};

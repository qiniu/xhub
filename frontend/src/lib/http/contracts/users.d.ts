export interface paths {
    "/user/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** 按权限分页列出用户 / List users within permission scope */
        get: operations["listUsers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/info": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 读取自己或可见用户 / Read self or visible user
         * @description 兼容GET对象正文中的user_id优先于query；建议使用query，普通用户只返回授权范围字段。 / Legacy GET object-body user_id overrides query; prefer query. Members receive authorized fields only.
         */
        get: operations["getUser"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v2/user/info": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 用户详情兼容别名 / User detail alias
         * @description 兼容GET对象正文中的user_id优先于query；建议使用query，普通用户只返回授权范围字段。 / Legacy GET object-body user_id overrides query; prefer query. Members receive authorized fields only.
         */
        get: operations["getUserV2"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/filter/ui": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** 管理员选择器最多500项 / Admin picker up to 500 users */
        get: operations["filterUsers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/available_users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** 管理员选择器兼容别名 / Admin picker alias */
        get: operations["availableUsers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/new": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** 创建用户并可绑定企业身份 / Create user with optional enterprise identity */
        post: operations["createUser"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** 按现有权限更新用户 / Update user with existing authorization */
        post: operations["updateUser"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/bulk_update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** 原子修改用户预算和刷新日 / Atomically update budgets and reset days */
        post: operations["bulkUpdateUserBudget"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** 删除用户，不允许删除自己 / Delete user except self */
        post: operations["deleteUser"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/set_password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key / Reset password under existing authorization and revoke sessions; no password returned, API keys retained
         * @description 按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key / Reset password under existing authorization and revoke sessions; no password returned, API keys retained
         */
        post: operations["setUserPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/{user_id}/password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key / Reset password under existing authorization and revoke sessions; no password returned, API keys retained
         * @description 按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key / Reset password under existing authorization and revoke sessions; no password returned, API keys retained
         */
        post: operations["setUserPasswordByPath"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        User: {
            user_id: string;
            user_email: string;
            user_alias: string;
            user_name: string;
            user_role: string;
            role: string;
            status: string;
            blocked: boolean;
            /** Format: double */
            max_budget: number | null;
            /** Format: double */
            spend: number;
            teams: components["schemas"]["UserTeam"][];
            /** @description 当前空占位，不是组织权限。 / Currently empty placeholder, not organization permissions. */
            organizations: unknown[];
            /** @description 当前空占位，不是可用模型列表。 / Currently empty placeholder, not available models. */
            models: string[];
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
            /** @description 当前固定0占位，不是实际密钥数。 / Currently zero placeholder, not actual key count. */
            key_count: number;
            sso_user_id: string | null;
            /** Format: double */
            tpm_limit: number | null;
            /** Format: double */
            rpm_limit: number | null;
            budget_reset_day: number;
            /** Format: date-time */
            budget_reset_at: string | null;
            budget_duration: string | null;
            metadata: {
                [key: string]: unknown;
            };
            sso_connection_id?: string;
            sso_connection_name?: string;
            sso_external_id?: string;
            sso_binding_managed?: boolean;
        };
        UserTeam: {
            team_id: string;
            team_alias: string;
            user_role: string;
            organization_id: string;
        };
        UserDetail: {
            user_info: components["schemas"]["User"];
        };
        /** @description 单人兼容有限非负数字字符串；空字符串/null清空，省略保留（创建时不限额），0禁止消费；字符串的语义由服务校验。 / Single-user compatibility: finite nonnegative numeric strings; empty string/null clears, omission preserves (uncapped on create), zero denies spend. String semantics are checked by the service. */
        LegacyBudget: (number | null) | string;
        /** @description 非负整数至2147483647，可用数字字符串，null/空字符串清空。 / Nonnegative integer up to 2147483647, numeric strings accepted; null/empty string clears. */
        LegacyRate: (number | null) | string;
        UserList: {
            total: number;
            total_count: number;
            page: number;
            current_page: number;
            total_pages: number;
            page_size: number;
            size: number;
            users: components["schemas"]["User"][];
        };
        UserPicker: {
            users: {
                user_id: string;
                user_email: string;
                user_alias: string;
            }[];
        };
        /** @description 平台管理员创建，不自动生成密钥；角色省略为user，兼容internal_user/proxy_admin；SSO两字段须同时提供；额外字段可能被忽略。 / Platform admin creates without a key; default role user, internal_user/proxy_admin aliases accepted; SSO pair must be supplied together; extra fields may be ignored. */
        CreateUser: {
            max_budget?: components["schemas"]["LegacyBudget"];
            /** @description 兼容校验但不写入；不用于设置团队成员预算。 / Validated compatibility field, not persisted; does not set team member budget. */
            max_budget_in_team?: components["schemas"]["LegacyBudget"];
            rpm_limit?: components["schemas"]["LegacyRate"];
            tpm_limit?: components["schemas"]["LegacyRate"];
            /** @description 0关闭；1至31按北京时间00:00刷新，短月取末日；省略保留，null非法。 / Zero disables; 1–31 resets at Beijing midnight, clamped to month end; omission preserves, null invalid. */
            budget_reset_day?: number;
            sso_connection_id?: string;
            sso_external_id?: string;
            /** @description 邮箱按既有规则去除首尾空白并转小写。 / Email is trimmed and lowercased by existing rules. */
            user_email: string;
            /** @description 8至72 UTF-8字节，不是字符数。 / 8–72 UTF-8 bytes, not characters. */
            password: string;
            user_role?: string;
            user_alias?: string;
            user_name?: string;
            team_id?: string;
            team_role?: string;
            admin_organization_ids?: string[];
            /** @description 设置组织管理员身份，不是普通组织归属。 / Assigns organization administration, not ordinary affiliation. */
            organization_id?: string;
        } & {
            [key: string]: unknown;
        };
        /** @description 完整单个JSON对象。目标省略为自己；普通用户只能改自己资料，管理字段不代表授予权限；预算写入单独授权。管理员密码与其他字段不是同一事务。 / Single complete JSON object. Target defaults to self; members edit own profile only, management fields do not grant privileges; budget writes separately authorized. Admin password and other fields are not one transaction. */
        UpdateUser: {
            max_budget?: components["schemas"]["LegacyBudget"];
            /** @description 兼容校验但不写入；不用于设置团队成员预算。 / Validated compatibility field, not persisted; does not set team member budget. */
            max_budget_in_team?: components["schemas"]["LegacyBudget"];
            rpm_limit?: components["schemas"]["LegacyRate"];
            tpm_limit?: components["schemas"]["LegacyRate"];
            /** @description 0关闭；1至31按北京时间00:00刷新，短月取末日；省略保留，null非法。 / Zero disables; 1–31 resets at Beijing midnight, clamped to month end; omission preserves, null invalid. */
            budget_reset_day?: number;
            sso_connection_id?: string;
            sso_external_id?: string;
            user_id?: string;
            user_email?: string;
            user_alias?: string;
            user_role?: string;
            blocked?: boolean;
            password?: string;
        } & {
            [key: string]: unknown;
        };
        BudgetChanges: {
            /** Format: double */
            max_budget: number | null;
            budget_reset_day?: number;
        } | {
            /** Format: double */
            max_budget?: number | null;
            budget_reset_day: number;
        };
        SelectedBudgetChange: {
            /** Format: double */
            max_budget: number | null;
            budget_reset_day?: number;
            user_id: string;
        } | {
            /** Format: double */
            max_budget?: number | null;
            budget_reset_day: number;
            user_id: string;
        };
        SelectedBudgetBatch: {
            /** @enum {boolean} */
            all_users?: false;
            users: components["schemas"]["SelectedBudgetChange"][];
        };
        AllBudgetBatch: {
            /** @enum {boolean} */
            all_users: true;
            user_updates: components["schemas"]["BudgetChanges"];
        };
        /** @description 平台管理员专用；最多1000用户、1MiB正文；ID不得重复，all_users包含禁用及管理员用户；原子失败不改变消费。 / Platform admin only; at most 1000 users and 1MiB body; unique IDs, all_users includes disabled/admin users; atomic failures do not change spend. */
        BulkBudgetInput: components["schemas"]["SelectedBudgetBatch"] | components["schemas"]["AllBudgetBatch"];
        BulkBudgetResult: {
            /** @enum {boolean} */
            atomic: true;
            batch_id: string;
            total_requested: number;
            successful_updates: number;
            /** @enum {integer} */
            failed_updates: 0;
            results: {
                user_id: string;
                user_email: string;
                /** @enum {boolean} */
                success: true;
                updated_user: components["schemas"]["User"];
            }[];
        };
        DeleteUser: {
            user_id: string;
        };
        DeletedUser: {
            /** @enum {boolean} */
            deleted: true;
        };
        /** @description 管理接口错误信封；HTTP 状态与 code 一致，不用成功对象代替失败。 / Management error envelope; code matches HTTP status and failures never use a success object. */
        Error: {
            error: components["schemas"]["ErrorDetail"];
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error401: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error403: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error500: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error503: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error400: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error404: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        ErrorDetail: {
            code: string;
            message: string;
            type: string;
            /** @description 当前为 null。 / Currently null. */
            param: unknown;
        };
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error409: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        SetPassword: {
            user_id?: string;
            /** @description 8–72 UTF-8字节，由服务校验 / 8–72 UTF-8 bytes, validated by server */
            password: string;
        };
        SetPasswordByBody: {
            user_id: string;
            /** @description 8–72 UTF-8字节，由服务校验 / 8–72 UTF-8 bytes, validated by server */
            password: string;
        };
        PasswordUpdated: {
            user_id: string;
            /** @enum {boolean} */
            password_updated: true;
        };
    };
    responses: {
        /** @description 失败HTTP 400，不得当成功。 / Failure HTTP 400, never success. */
        Error400: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error400"];
            };
        };
        /** @description 失败HTTP 401，不得当成功。 / Failure HTTP 401, never success. */
        Error401: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error401"];
            };
        };
        /** @description 失败HTTP 403，不得当成功。 / Failure HTTP 403, never success. */
        Error403: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error403"];
            };
        };
        /** @description 失败HTTP 404，不得当成功。 / Failure HTTP 404, never success. */
        Error404: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error404"];
            };
        };
        /** @description 失败HTTP 409，不得当成功。 / Failure HTTP 409, never success. */
        Error409: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error409"];
            };
        };
        /** @description 失败HTTP 500，不得当成功。 / Failure HTTP 500, never success. */
        Error500: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error500"];
            };
        };
        /** @description 失败HTTP 503，不得当成功。 / Failure HTTP 503, never success. */
        Error503: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error503"];
            };
        };
    };
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    listUsers: {
        parameters: {
            query?: {
                /** @description 按当前解析：无效/小于1的page回退1，page_size超出1..500回退100。 / Current parsing: invalid/page below 1 defaults to 1; page_size outside 1..500 defaults to 100. */
                page?: string;
                /** @description 按当前解析：无效/小于1的page回退1，page_size超出1..500回退100。 / Current parsing: invalid/page below 1 defaults to 1; page_size outside 1..500 defaults to 100. */
                page_size?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                search?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_email?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_ids?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                role?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                team?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                sso_user_ids?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                organization_ids?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                sort_by?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                sort_order?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserList"];
                };
            };
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    getUser: {
        parameters: {
            query?: {
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_id?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserDetail"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    getUserV2: {
        parameters: {
            query?: {
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_id?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserDetail"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    filterUsers: {
        parameters: {
            query?: {
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_id?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_email?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserPicker"];
                };
            };
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    availableUsers: {
        parameters: {
            query?: {
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_id?: string;
                /** @description 兼容查询字符串；授权及字段组合由服务执行。 / Compatibility query string; authorization and combinations checked by service. */
                user_email?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserPicker"];
                };
            };
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    createUser: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateUser"];
            };
        };
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["User"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    updateUser: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateUser"];
            };
        };
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserDetail"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    bulkUpdateUserBudget: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["BulkBudgetInput"];
            };
        };
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BulkBudgetResult"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    deleteUser: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeleteUser"];
            };
        };
        responses: {
            /** @description 真实业务结果 / Actual business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeletedUser"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    setUserPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SetPasswordByBody"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasswordUpdated"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    setUserPasswordByPath: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                user_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SetPassword"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PasswordUpdated"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
}

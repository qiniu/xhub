export interface paths {
    "/key/generate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 创建个人Key / Create a personal key
         * @description 仅创建返回明文；默认当前用户，团队由成员关系派生；空预算仍受个人与父级预算限制。支持契约为单个对象，旧handler还接受空正文/null，未声明兼容输入不承诺严格拒绝。 / Only creation returns plaintext; defaults to current user, team derived from membership; blank budget still obeys personal and parent caps. Supported contract is one object; legacy handler also accepts empty body or null and does not strictly reject every undeclared input.
         */
        post: operations["createKey"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/key/list": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 按权限列出Key / List keys within permission scope
         * @description scope=personal仅登录用户本人，管理员也不例外；非法分页/排序返回400，空列表总页数为1。status/expand等旧参数被忽略，不声明为有效筛选。 / scope=personal is the logged-in owner only, including admins; invalid pagination or sorting returns 400; empty results have one total page. Legacy status and expand parameters are ignored, not effective filters.
         */
        get: operations["listKeys"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/key/info": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 查询单个Key / Read one key
         * @description 查询单个Key / Read one key
         */
        get: operations["getKey"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v2/key/info": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 按正文查询单个Key / Read one key by body
         * @description 仅支持key，不支持keys数组；正文key覆盖查询参数，旧查询参数回退只记录兼容边界。 / Supports key, not a keys array; body key overrides query; legacy query fallback is a compatibility boundary.
         */
        post: operations["getKeyV2"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/key/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 更新Key并保留省略字段 / Update a key while preserving omitted fields
         * @description 返回Key对象，无data包装；不返回明文，不改变归属与历史消费。 / Returns the key object without a data envelope; no plaintext, ownership or historical spend changes.
         */
        post: operations["updateKey"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/key/block": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 禁用Key / Block a key
         * @description 禁用Key / Block a key
         */
        post: operations["blockKey"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/key/unblock": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 恢复Key / Unblock a key
         * @description 恢复Key / Unblock a key
         */
        post: operations["unblockKey"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/key/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 删除一个或多个Key / Delete one or more keys
         * @description 先完成所有目标权限检查，再逐个删除；数据库中途失败可能部分成功，不是整批事务；重复删除404。 / Authorizes every target before deleting individually; storage failure may leave partial deletion, not a batch transaction; repeated deletion returns 404.
         */
        post: operations["deleteKeys"];
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
        /** @description 缺省保留（创建不设独立上限）；null/空串清空；0禁止此Key消费；字符串须为有限非负数 / Omission preserves (creation has no separate cap); null or empty string clears; zero blocks this key; strings must be finite nonnegative numbers. */
        BudgetInput: (number | string) | null;
        /** @description null/空串取消；字符串须为非负int32整数 / Null or empty clears; strings must represent nonnegative int32 integers. */
        RateInput: (number | string) | null;
        CreateKey: {
            key_alias?: string;
            /** @description 空数组或全部模型哨兵表示继承，不扩大父级权限 / Empty array or all-model sentinel inherits without widening parent permissions. */
            models?: string[] | null;
            max_budget?: components["schemas"]["BudgetInput"];
            rpm_limit?: components["schemas"]["RateInput"];
            tpm_limit?: components["schemas"]["RateInput"];
            /** @description 省略保留；null/空串清除并继承 / Omission preserves; null or empty clears and inherits. */
            route_template_id?: string | null;
            /** @description 创建可省略；更新省略/null/空串保留旧期限，不清除；非空须有效正时长如30d / Optional at creation; omission, null or empty preserves expiry on update; nonempty must be a valid positive duration such as 30d. */
            duration?: string | null;
            /** @enum {string|null} */
            key_type?: "llm_api" | "" | null;
            allowed_routes?: "llm_api_routes"[] | null;
            /** @enum {boolean|null} */
            auto_rotate?: false | null;
            /** @enum {string|null} */
            rotation_interval?: "" | null;
            /** @enum {string|null} */
            grace_period?: "" | null;
            key_name?: string;
            /** @enum {string} */
            owner_type?: "personal" | "";
            user_id?: string;
            team_id?: string | null;
            project_id?: string;
        };
        UpdateKey: {
            key: string;
            key_alias?: string;
            /** @description 空数组或全部模型哨兵表示继承，不扩大父级权限 / Empty array or all-model sentinel inherits without widening parent permissions. */
            models?: string[] | null;
            max_budget?: components["schemas"]["BudgetInput"];
            rpm_limit?: components["schemas"]["RateInput"];
            tpm_limit?: components["schemas"]["RateInput"];
            /** @description 省略保留；null/空串清除并继承 / Omission preserves; null or empty clears and inherits. */
            route_template_id?: string | null;
            /** @description 创建可省略；更新省略/null/空串保留旧期限，不清除；非空须有效正时长如30d / Optional at creation; omission, null or empty preserves expiry on update; nonempty must be a valid positive duration such as 30d. */
            duration?: string | null;
            /** @enum {string|null} */
            key_type?: "llm_api" | "" | null;
            allowed_routes?: "llm_api_routes"[] | null;
            /** @enum {boolean|null} */
            auto_rotate?: false | null;
            /** @enum {string|null} */
            rotation_interval?: "" | null;
            /** @enum {string|null} */
            grace_period?: "" | null;
        };
        KeySelector: {
            key: string;
        };
        DeleteKeys: {
            keys: string[];
            key?: string;
        } | {
            key: string;
            keys?: string[];
        };
        KeyRecord: {
            token_id: string;
            /** @description 创建为明文；普通列表/详情为遮罩；个人列表为记录ID，不可用其调用模型 / Plaintext on creation; masked on ordinary list and detail; record ID on personal list, not an inference credential. */
            token: string;
            key_name: string;
            key_alias: string;
            key_prefix: string;
            owner_type: string;
            status: string;
            user_id: string | null;
            team_id: string | null;
            project_id: string | null;
            created_by: string | null;
            route_template_id: string | null;
            /** @enum {string} */
            key_type: "llm_api";
            allowed_routes: "llm_api_routes"[];
            /** @enum {boolean} */
            auto_rotate: false;
            /** @enum {string|null} */
            rotation_interval: null;
            models: string[];
            /** Format: double */
            max_budget: number | null;
            /** Format: double */
            spend: number;
            rpm_limit: number | null;
            tpm_limit: number | null;
            blocked: boolean;
            /** Format: date-time */
            expires: string | null;
            /** Format: date-time */
            last_active: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
        };
        CreatedKey: {
            token_id: string;
            /** @description 创建为明文；普通列表/详情为遮罩；个人列表为记录ID，不可用其调用模型 / Plaintext on creation; masked on ordinary list and detail; record ID on personal list, not an inference credential. */
            token: string;
            key_name: string;
            key_alias: string;
            key_prefix: string;
            owner_type: string;
            status: string;
            user_id: string | null;
            team_id: string | null;
            project_id: string | null;
            created_by: string | null;
            route_template_id: string | null;
            /** @enum {string} */
            key_type: "llm_api";
            allowed_routes: "llm_api_routes"[];
            /** @enum {boolean} */
            auto_rotate: false;
            /** @enum {string|null} */
            rotation_interval: null;
            models: string[];
            /** Format: double */
            max_budget: number | null;
            /** Format: double */
            spend: number;
            rpm_limit: number | null;
            tpm_limit: number | null;
            blocked: boolean;
            /** Format: date-time */
            expires: string | null;
            /** Format: date-time */
            last_active: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
            key: string;
        };
        BudgetReservation: {
            reservation_id: string;
            call_id: string;
            kind: string;
            node_id: string;
            key_id: string;
            user_id: string;
            team_id: string;
            org_id: string;
            project_id: string;
            snapshot: string;
            state: string;
            reason: string;
            /** Format: double */
            amount: number;
            /** Format: date-time */
            started_at: string;
            /** Format: date-time */
            created_at: string;
        };
        KeyInfoRecord: {
            token_id: string;
            /** @description 创建为明文；普通列表/详情为遮罩；个人列表为记录ID，不可用其调用模型 / Plaintext on creation; masked on ordinary list and detail; record ID on personal list, not an inference credential. */
            token: string;
            key_name: string;
            key_alias: string;
            key_prefix: string;
            owner_type: string;
            status: string;
            user_id: string | null;
            team_id: string | null;
            project_id: string | null;
            created_by: string | null;
            route_template_id: string | null;
            /** @enum {string} */
            key_type: "llm_api";
            allowed_routes: "llm_api_routes"[];
            /** @enum {boolean} */
            auto_rotate: false;
            /** @enum {string|null} */
            rotation_interval: null;
            models: string[];
            /** Format: double */
            max_budget: number | null;
            /** Format: double */
            spend: number;
            rpm_limit: number | null;
            tpm_limit: number | null;
            blocked: boolean;
            /** Format: date-time */
            expires: string | null;
            /** Format: date-time */
            last_active: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
            /** Format: double */
            budget_pending: number;
            budget_reservations: components["schemas"]["BudgetReservation"][] | null;
        };
        KeyInfo: {
            info: components["schemas"]["KeyInfoRecord"];
        };
        KeyList: {
            keys: components["schemas"]["KeyRecord"][];
            total_count: number;
            current_page: number;
            total_pages: number;
            size: number;
        };
        DeletedKeys: {
            deleted: number;
        };
        BlockedKey: {
            /** @enum {boolean} */
            blocked: true;
        };
        UnblockedKey: {
            /** @enum {boolean} */
            blocked: false;
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
    createKey: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateKey"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CreatedKey"];
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
    listKeys: {
        parameters: {
            query?: {
                scope?: "personal";
                page?: number;
                size?: number;
                sort_by?: "created_at" | "updated_at" | "key_alias" | "token" | "spend" | "max_budget" | "budget_remaining" | "budget_utilization";
                sort_order?: "asc" | "desc";
                team_id?: string;
                project_id?: string;
                user_id?: string;
                search?: string;
                key_alias?: string;
                /** @description 历史命名，实际按记录ID筛选 / Legacy name, actually filters by record ID. */
                key_hash?: string;
                substring_matching?: boolean;
                /** @description 按密钥所属用户当前团队的组织精确筛选，与权限范围及其他条件取交集；空值不限制组织，非空时无团队 Key 不匹配。 / Exact match on the key owner's current team organization; intersected with authorization and other filters. Empty means no restriction; teamless keys do not match a nonempty value. */
                organization_id?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["KeyList"];
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
    getKey: {
        parameters: {
            query: {
                key: string;
                scope?: "personal";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["KeyInfo"];
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
    getKeyV2: {
        parameters: {
            query?: {
                scope?: "personal";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KeySelector"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["KeyInfo"];
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
    updateKey: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateKey"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["KeyRecord"];
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
    blockKey: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KeySelector"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["BlockedKey"];
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
    unblockKey: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["KeySelector"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UnblockedKey"];
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
    deleteKeys: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeleteKeys"];
            };
        };
        responses: {
            /** @description 实际结果 / Actual result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeletedKeys"];
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

export interface paths {
    "/spend/logs/v2": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 / Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed
         * @description 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 / Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed
         */
        get: operations["listLogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/spend/logs/ui": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 / Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed
         * @description 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 / Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed
         */
        get: operations["listUILogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/spend/logs/session/ui": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 / Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed；session_id空时既有接口在鉴权前返回空页 / Empty session_id returns empty page before authentication under existing behavior
         * @description 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 / Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed；session_id空时既有接口在鉴权前返回空页 / Empty session_id returns empty page before authentication under existing behavior
         */
        get: operations["listSessionLogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/spend/logs/ui/{request_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 范围外与不存在均404；管理员读取他人详情记审计；正文缺失可为空，非JSON上游响应为字符串；并非所有错误均有账单 / Out-of-scope and missing records are 404; admin cross-user reads audited; missing bodies may be empty and non-JSON upstream bodies strings; not every failure has a usage record
         * @description 范围外与不存在均404；管理员读取他人详情记审计；正文缺失可为空，非JSON上游响应为字符串；并非所有错误均有账单 / Out-of-scope and missing records are 404; admin cross-user reads audited; missing bodies may be empty and non-JSON upstream bodies strings; not every failure has a usage record
         */
        get: operations["getLog"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/daily/activity": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         * @description 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         */
        get: operations["userDailyUsage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/user/daily/activity/aggregated": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         * @description 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         */
        get: operations["userTotalUsage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/team/daily/activity": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         * @description 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         */
        get: operations["teamDailyUsage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/team/daily/activity/aggregated": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         * @description 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         */
        get: operations["teamTotalUsage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/organization/daily/activity": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         * @description 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 / Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted
         */
        get: operations["organizationDailyUsage"];
        put?: never;
        post?: never;
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
        LogRecord: {
            request_id: string;
            api_key: string;
            team_id: string;
            model: string;
            model_id: string;
            call_type: string;
            reasoning_effort: string;
            startTime: string;
            endTime: string;
            inbound_endpoint: string;
            outbound_endpoint: string;
            user: string;
            cache_hit: string;
            cache_key: string;
            session_id: string;
            session_source: string;
            status: string;
            owner_type: string;
            project_id: string;
            organization_id: string;
            custom_llm_provider: string;
            credential_name: string;
            api_base: string;
            label: string;
            spend: number;
            total_tokens: number;
            prompt_tokens: number;
            completion_tokens: number;
            request_duration_ms: number;
            streamed: boolean;
            ttft_ms: number | null;
            tps: number | null;
            /** @description 保留任意JSON类型、未知字段与null / Preserve arbitrary JSON values, unknown fields and null */
            messages: unknown;
            /** @description 保留任意JSON类型、未知字段与null / Preserve arbitrary JSON values, unknown fields and null */
            response: unknown;
            metadata: {
                [key: string]: unknown;
            };
            completionStartTime?: string;
            cache_read_input_tokens?: number;
            error?: string;
            /** @description 保留任意JSON类型、未知字段与null / Preserve arbitrary JSON values, unknown fields and null */
            proxy_server_request?: unknown;
            session_total_count?: number;
            session_llm_count?: number;
            session_spend?: number;
            session_total_tokens?: number;
        } & {
            [key: string]: unknown;
        };
        LogPage: {
            data: components["schemas"]["LogRecord"][];
            total: number;
            page: number;
            page_size: number;
            total_pages: number;
        };
        UsageMetrics: {
            spend: number;
            flat_cost: number;
            prompt_tokens: number;
            completion_tokens: number;
            total_tokens: number;
            api_requests: number;
            successful_requests: number;
            failed_requests: number;
            compression_saved_tokens: number;
            compression_savings_spend: number;
            prompt_caching_savings_spend: number;
            gateway_injected_caching_savings_spend: number;
            autorouter_savings_spend: number;
            cache_read_input_tokens?: number;
        };
        UsageBucket: {
            metrics: components["schemas"]["UsageMetrics"];
            metadata: {
                [key: string]: unknown;
            };
            api_key_breakdown: {
                [key: string]: components["schemas"]["UsageKeyBucket"];
            };
        };
        UsageBreakdown: {
            models: {
                [key: string]: components["schemas"]["UsageBucket"];
            };
            model_groups: {
                [key: string]: components["schemas"]["UsageBucket"];
            };
            providers: {
                [key: string]: components["schemas"]["UsageBucket"];
            };
            api_keys: {
                [key: string]: components["schemas"]["UsageKeyBucket"];
            };
            endpoints: {
                [key: string]: components["schemas"]["UsageBucket"];
            };
            mcp_servers: {
                [key: string]: components["schemas"]["UsageBucket"];
            };
            entities: {
                [key: string]: components["schemas"]["UsageBucket"];
            };
        };
        UsageDay: {
            date: string;
            metrics: components["schemas"]["UsageMetrics"];
            breakdown: components["schemas"]["UsageBreakdown"];
        };
        UsageMetadata: {
            total_spend: number;
            total_flat_cost: number;
            total_prompt_tokens: number;
            total_completion_tokens: number;
            total_api_requests: number;
            total_successful_requests: number;
            total_failed_requests: number;
            total_compression_saved_tokens: number;
            total_compression_savings_spend: number;
            total_prompt_caching_savings_spend: number;
            total_gateway_injected_caching_savings_spend: number;
            total_autorouter_savings_spend: number;
            total_cache_read_input_tokens?: number;
            total_pages: number;
            page: number;
            has_more: boolean;
            total_tokens: number;
        };
        DailyUsage: {
            results: components["schemas"]["UsageDay"][];
            metadata: components["schemas"]["UsageMetadata"];
        };
        UsageKeyMetadata: {
            key_alias: string | null;
            team_id: string | null;
            user_id: string | null;
            user_email: string | null;
        };
        UsageKeyBucket: {
            metrics: components["schemas"]["UsageMetrics"];
            metadata: components["schemas"]["UsageKeyMetadata"];
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
    listLogs: {
        parameters: {
            query?: {
                /** @description 支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天 / Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day. */
                start_date?: string;
                /** @description 支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天 / Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day. */
                end_date?: string;
                page?: string;
                page_size?: string;
                user_id?: string;
                team_id?: string;
                api_key?: string;
                model?: string;
                session_id?: string;
                request_id?: string;
                search?: string;
                error_code?: string;
                error_message?: string;
                sort_by?: string;
                sort_order?: string;
                status_filter?: string;
                group_by_session?: string;
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
                    "application/json": components["schemas"]["LogPage"];
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
    listUILogs: {
        parameters: {
            query?: {
                /** @description 支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天 / Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day. */
                start_date?: string;
                /** @description 支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天 / Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day. */
                end_date?: string;
                page?: string;
                page_size?: string;
                user_id?: string;
                team_id?: string;
                api_key?: string;
                model?: string;
                session_id?: string;
                request_id?: string;
                search?: string;
                error_code?: string;
                error_message?: string;
                sort_by?: string;
                sort_order?: string;
                status_filter?: string;
                group_by_session?: string;
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
                    "application/json": components["schemas"]["LogPage"];
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
    listSessionLogs: {
        parameters: {
            query?: {
                /** @description 支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天 / Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day. */
                start_date?: string;
                /** @description 支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天 / Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day. */
                end_date?: string;
                page?: string;
                page_size?: string;
                user_id?: string;
                team_id?: string;
                api_key?: string;
                model?: string;
                session_id?: string;
                request_id?: string;
                search?: string;
                error_code?: string;
                error_message?: string;
                sort_by?: string;
                sort_order?: string;
                status_filter?: string;
                group_by_session?: string;
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
                    "application/json": components["schemas"]["LogPage"];
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
    getLog: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                request_id: string;
            };
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
                    "application/json": components["schemas"]["LogRecord"];
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
    userDailyUsage: {
        parameters: {
            query?: {
                start_date?: string;
                end_date?: string;
                page?: string;
                timezone?: string;
                user_id?: string;
                team_id?: string;
                team_ids?: string;
                exclude_team_ids?: string;
                organization_ids?: string;
                api_key?: string;
                model?: string;
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
                    "application/json": components["schemas"]["DailyUsage"];
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
    userTotalUsage: {
        parameters: {
            query?: {
                start_date?: string;
                end_date?: string;
                page?: string;
                timezone?: string;
                user_id?: string;
                team_id?: string;
                team_ids?: string;
                exclude_team_ids?: string;
                organization_ids?: string;
                api_key?: string;
                model?: string;
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
                    "application/json": components["schemas"]["DailyUsage"];
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
    teamDailyUsage: {
        parameters: {
            query?: {
                start_date?: string;
                end_date?: string;
                page?: string;
                timezone?: string;
                user_id?: string;
                team_id?: string;
                team_ids?: string;
                exclude_team_ids?: string;
                organization_ids?: string;
                api_key?: string;
                model?: string;
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
                    "application/json": components["schemas"]["DailyUsage"];
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
    teamTotalUsage: {
        parameters: {
            query?: {
                start_date?: string;
                end_date?: string;
                page?: string;
                timezone?: string;
                user_id?: string;
                team_id?: string;
                team_ids?: string;
                exclude_team_ids?: string;
                organization_ids?: string;
                api_key?: string;
                model?: string;
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
                    "application/json": components["schemas"]["DailyUsage"];
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
    organizationDailyUsage: {
        parameters: {
            query?: {
                start_date?: string;
                end_date?: string;
                page?: string;
                timezone?: string;
                user_id?: string;
                team_id?: string;
                team_ids?: string;
                exclude_team_ids?: string;
                organization_ids?: string;
                api_key?: string;
                model?: string;
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
                    "application/json": components["schemas"]["DailyUsage"];
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

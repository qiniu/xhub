export interface paths {
    "/v2/model/info": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 读取部署列表 / List deployments
         * @description 仅平台管理员；未知ID为空列表；其他旧筛选参数被忽略 / Platform admin only; unknown ID returns empty list; other legacy filters are ignored
         */
        get: operations["listDeployments"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/model/groups": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * 读取公开模型分组 / List public model groups
         * @description 读取公开模型分组 / List public model groups
         */
        get: operations["listModelGroups"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/model/new": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 创建数据库部署 / Create stored deployment
         * @description 声明已支持输入；附加跨字段校验由真实服务执行，未安装运行时严格校验 / Declares supported input; cross-field checks remain in real service, no strict runtime middleware
         */
        post: operations["createDeployment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/model/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 合并部署补丁 / Merge deployment patch
         * @description 省略保持；星号遮罩不覆盖密钥；ID从model_info.id或id读取 / Omitted fields retained; masked secrets preserved; ID from model_info.id or id
         */
        post: operations["updateDeployment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/model/{model_id}/update": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * 按路径更新部署 / Update deployment by path
         * @description 按路径更新部署 / Update deployment by path
         */
        patch: operations["patchDeployment"];
        trace?: never;
    };
    "/model/delete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 删除部署 / Delete deployment
         * @description 删除部署 / Delete deployment
         */
        post: operations["deleteDeployment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/model/disable": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 停用部署 / Disable deployment
         * @description 停用部署 / Disable deployment
         */
        post: operations["disableDeployment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/model/enable": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 启用部署 / Enable deployment
         * @description 启用部署 / Enable deployment
         */
        post: operations["enableDeployment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/health/test_connection": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * 探测保存的部署 / Probe stored deployment
         * @description 只使用保存的地址和凭据；失败也可为HTTP200，必须检查healthy；不经过预算和记账 / Uses stored URL and credentials only; HTTP200 may be unhealthy; bypasses budget and billing
         */
        post: operations["probeDeployment"];
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
        /** @description 上游参数允许扩展；读取时敏感顶层字符串打码，不能认为所有嵌套字段均脱敏 / Extensible upstream parameters; sensitive top-level strings are masked, not arbitrary nested fields */
        Parameters: {
            model?: string;
            custom_llm_provider?: string;
            litellm_credential_name?: string;
            api_base?: string;
            api_key?: string;
            /** @description 兼容有限非负数字字符串或null；字符串数值由服务检查 / Finite nonnegative numeric strings or null accepted; service validates string values */
            input_cost_per_token?: number | (string | null);
            /** @description 兼容有限非负数字字符串或null；字符串数值由服务检查 / Finite nonnegative numeric strings or null accepted; service validates string values */
            output_cost_per_token?: number | (string | null);
        } & {
            [key: string]: unknown;
        };
        ModelInfo: {
            id?: string;
            transport?: string;
            endpoint_types?: string[];
            pricing_source?: string;
            base_model?: string;
            disabled?: boolean;
            db_model?: boolean;
            unavailable_reason?: string;
        } & {
            [key: string]: unknown;
        };
        Deployment: {
            model_name: string;
            litellm_params: components["schemas"]["Parameters"];
            model_info: components["schemas"]["PublicModelInfo"];
        };
        DeploymentRow: {
            model_name: string;
            litellm_params: components["schemas"]["Parameters"];
            model_info: components["schemas"]["PublicModelInfo"];
            id: string;
            db_model: boolean;
        };
        DeletedDeployment: {
            model_name: string;
            litellm_params: components["schemas"]["Parameters"];
            model_info: components["schemas"]["PublicModelInfo"];
            id: string;
            /** @enum {boolean} */
            deleted: true;
        };
        DeploymentList: {
            data: components["schemas"]["DeploymentRow"][];
            total_count: number;
            current_page: number;
            total_pages: number;
            size: number;
        };
        DefaultWeights: {
            allocations: {
                deployment_id: string;
                /** Format: double */
                weight: number;
            }[] | null;
        };
        FallbackPolicy: {
            fallbacks: string[] | null;
            context_window_fallbacks: string[] | null;
            content_policy_fallbacks: string[] | null;
        };
        ModelGroup: {
            model_name: string;
            deployments: components["schemas"]["DeploymentRow"][];
            default_weights: components["schemas"]["DefaultWeights"];
            fallback_policy: components["schemas"]["FallbackPolicy"];
        };
        GroupList: {
            data: components["schemas"]["ModelGroup"][];
            total_count: number;
            current_page: number;
            total_pages: number;
        };
        CreateDeployment: {
            model_name: string;
            litellm_params: components["schemas"]["Parameters"];
            model_info: components["schemas"]["ModelInfo"];
        } & {
            [key: string]: unknown;
        };
        UpdateDeployment: {
            id?: string;
            model_name?: string;
            litellm_params?: components["schemas"]["Parameters"];
            model_info?: components["schemas"]["ModelInfo"];
            disabled?: boolean;
        } & {
            [key: string]: unknown;
        };
        DeploymentSelector: ({
            id?: string;
            model?: string;
            model_name?: string;
        } & {
            [key: string]: unknown;
        }) | {
            id: string;
        } | {
            model: string;
        } | {
            model_name: string;
        };
        ConnectionRequest: {
            model_info: {
                id: string;
            } & {
                [key: string]: unknown;
            };
            litellm_params?: components["schemas"]["Parameters"];
            model?: string;
            mode?: string;
        } & {
            [key: string]: unknown;
        };
        ConnectionSuccess: {
            /** @enum {string} */
            status: "success";
            /** @enum {boolean} */
            healthy: true;
            /** @enum {string|null} */
            error: null;
            model: string;
            mode: string;
        };
        ConnectionFailure: {
            /** @enum {string} */
            status: "error";
            /** @enum {boolean} */
            healthy: false;
            error: string;
            message: string;
            result: {
                error: string;
            };
            model: string;
            mode: string;
        };
        ConnectionResult: components["schemas"]["ConnectionSuccess"] | components["schemas"]["ConnectionFailure"];
        /** @description 管理接口错误信封；HTTP 状态与 code 一致，不用成功对象代替失败。 / Management error envelope; code matches HTTP status and failures never use a success object. */
        Error: {
            error: components["schemas"]["ErrorDetail"];
        };
        ErrorDetail: {
            code: string;
            message: string;
            type: string;
            /** @description 当前为 null。 / Currently null. */
            param: unknown;
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
        /** @description 完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。 / Error fields are combined at the same object level to avoid replacing the nested error. */
        Error409: {
            error: components["schemas"]["ErrorDetail"] & {
                code: string;
            };
        };
        /** @description 读取时保证稳定ID和状态；其他扩展保持原值 / Stable ID and status on read; extensions preserved */
        PublicModelInfo: {
            id: string;
            transport?: string;
            endpoint_types?: string[];
            pricing_source?: string;
            base_model?: string;
            disabled: boolean;
            db_model: boolean;
            unavailable_reason?: string;
        } & {
            [key: string]: unknown;
        };
        DeleteSelector: ({
            id?: string;
            model_name?: string;
        } & {
            [key: string]: unknown;
        }) | {
            id: string;
        } | {
            model_name: string;
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
    listDeployments: {
        parameters: {
            query?: {
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                modelId?: string;
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                search?: string;
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                page?: string;
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                size?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeploymentList"];
                };
            };
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
        };
    };
    listModelGroups: {
        parameters: {
            query?: {
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                search?: string;
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                page?: string;
                /** @description 保留既有字符串解析 / Existing string parsing retained */
                size?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["GroupList"];
                };
            };
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    createDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateDeployment"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Deployment"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
        };
    };
    updateDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateDeployment"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Deployment"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
        };
    };
    patchDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                model_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["UpdateDeployment"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Deployment"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
        };
    };
    deleteDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeleteSelector"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeletedDeployment"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            409: components["responses"]["Error409"];
            500: components["responses"]["Error500"];
        };
    };
    disableDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeploymentSelector"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Deployment"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
        };
    };
    enableDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["DeploymentSelector"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Deployment"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
        };
    };
    probeDeployment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ConnectionRequest"];
            };
        };
        responses: {
            /** @description 成功或显式业务结果 / Success or explicit business result */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConnectionResult"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            404: components["responses"]["Error404"];
        };
    };
}

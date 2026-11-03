export interface paths {
    "/credentials": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** 列出供应商（两个等价列表字段）。 / List suppliers (two equivalent array aliases). */
        get: operations["listCredentials"];
        put?: never;
        /** 保存供应商，同名创建覆盖已有配置。 / Save supplier; duplicate creation replaces existing configuration. */
        post: operations["createCredential"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/credentials/by_name/{credential_name}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** 读取供应商，敏感字符串脱敏。 / Read supplier with masked secret strings. */
        get: operations["getCredential"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/credentials/{credential_name}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** 删除供应商；重复删除返回 deleted=0。 / Delete supplier; repeated deletion returns deleted=0. */
        delete: operations["deleteCredential"];
        options?: never;
        head?: never;
        /** 合并补丁；不存在则创建。 / Merge patch; upsert when missing. */
        patch: operations["updateCredential"];
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        /** @description 创建/更新 JSON 对象；没有必填业务字段，扩展字段保留，password 顶层字段忽略，jsonrpc 顶层字段拒绝。 / Create/update object; no required business fields, extensions retained, top-level password ignored and jsonrpc rejected. */
        CredentialInput: {
            /** @description 供应商名；创建缺省或非字符串时生成 ID。 / Name; generated when absent or non-string on create. */
            credential_name?: unknown;
            /** @description 供应商元数据，历史接口允许任意 JSON。 / Provider metadata; legacy API accepts any JSON. */
            credential_info?: unknown;
            /** @description 凭据字段；对象内敏感字符串读取时脱敏，创建响应省略对象。PATCH 忽略空值或含 ** 的敏感字段，非对象补丁忽略。 / Credential object is masked on read and omitted on create. PATCH ignores blank/masked secret fields and non-object values. */
            credential_values?: unknown;
            /** @description 创建时间；历史接口允许调用者覆盖。 / Creation time; legacy caller overrides are accepted. */
            created_at?: unknown;
        } & {
            [key: string]: unknown;
        };
        /** @description 供应商读取或保存结果；管理接口返回，Go 原始 JSON 类型保留扩展字段及 null。 / Supplier read/save result; raw JSON Go representation preserves extensions and nulls. */
        Credential: {
            /** @description 供应商元数据，历史接口允许任意 JSON。 / Provider metadata; legacy API accepts any JSON. */
            credential_info: unknown;
            /** @description 凭据字段；对象内敏感字符串读取时脱敏，创建响应省略对象。PATCH 忽略空值或含 ** 的敏感字段，非对象补丁忽略。 / Credential object is masked on read and omitted on create. PATCH ignores blank/masked secret fields and non-object values. */
            credential_values?: unknown;
            /** @description 创建时间；历史接口允许调用者覆盖。 / Creation time; legacy caller overrides are accepted. */
            created_at?: unknown;
            id: string;
            credential_name: string;
        } & {
            [key: string]: unknown;
        };
        /** @description 供应商列表响应；data 与 credentials 是同一列表的兼容别名。 / Supplier list response; data and credentials are equivalent compatibility aliases. */
        CredentialList: {
            /** @enum {string} */
            object: "list";
            data: components["schemas"]["Credential"][];
            credentials: components["schemas"]["Credential"][];
        };
        /** @description 删除结果；deleted 为实际删除条数，重复删除可为零。 / Deletion result; deleted counts actual removals and may be zero on repeats. */
        Deletion: {
            id: string;
            credential_name: string;
            deleted: number;
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
    };
    responses: {
        /** @description 未登录或凭据无效。 / Missing or invalid authentication. */
        Error401: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error401"];
            };
        };
        /** @description 非管理员。 / Administrator required. */
        Error403: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error403"];
            };
        };
        /** @description 持久化失败，不伪造成功。 / Storage failure, never success. */
        Error500: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error500"];
            };
        };
        /** @description 存储不可用。 / Store unavailable. */
        Error503: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error503"];
            };
        };
        /** @description 非法 JSON 或 REST 端点收到 JSON-RPC。 / Invalid JSON or JSON-RPC on REST. */
        Error400: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error400"];
            };
        };
        /** @description 记录不存在。 / Record not found. */
        Error404: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error404"];
            };
        };
    };
    parameters: {
        /** @description 普通资源名称。保留动作词和集合别名不在本试点范围，参见边界文档。 / Ordinary resource name; reserved action/collection aliases are outside the pilot. */
        CredentialName: string;
    };
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    listCredentials: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 成功。 / Success. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CredentialList"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    createCredential: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** @description 空正文沿用空对象语义；正文存在时必须是 JSON 对象。 / Empty body acts as an empty object; nonempty body must be a JSON object. */
        requestBody?: {
            content: {
                /**
                 * @example {
                 *       "credential_name": "example-provider",
                 *       "credential_info": {
                 *         "custom_llm_provider": "openai"
                 *       },
                 *       "credential_values": {
                 *         "api_base": "https://provider.example/v1",
                 *         "api_key": "example-not-a-secret"
                 *       }
                 *     }
                 */
                "application/json": components["schemas"]["CredentialInput"];
            };
        };
        responses: {
            /** @description 成功。 / Success. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Credential"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    getCredential: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description 普通资源名称。保留动作词和集合别名不在本试点范围，参见边界文档。 / Ordinary resource name; reserved action/collection aliases are outside the pilot. */
                credential_name: components["parameters"]["CredentialName"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description 成功。 / Success. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Credential"];
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
    deleteCredential: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description 普通资源名称。保留动作词和集合别名不在本试点范围，参见边界文档。 / Ordinary resource name; reserved action/collection aliases are outside the pilot. */
                credential_name: components["parameters"]["CredentialName"];
            };
            cookie?: never;
        };
        /** @description 空正文沿用空对象语义；正文存在时必须是 JSON 对象。 / Empty body acts as an empty object; nonempty body must be a JSON object. */
        requestBody?: {
            content: {
                /**
                 * @example {
                 *       "credential_name": "example-provider",
                 *       "credential_info": {
                 *         "custom_llm_provider": "openai"
                 *       },
                 *       "credential_values": {
                 *         "api_base": "https://provider.example/v1",
                 *         "api_key": "example-not-a-secret"
                 *       }
                 *     }
                 */
                "application/json": components["schemas"]["CredentialInput"];
            };
        };
        responses: {
            /** @description 成功。 / Success. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Deletion"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
    updateCredential: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description 普通资源名称。保留动作词和集合别名不在本试点范围，参见边界文档。 / Ordinary resource name; reserved action/collection aliases are outside the pilot. */
                credential_name: components["parameters"]["CredentialName"];
            };
            cookie?: never;
        };
        /** @description 空正文沿用空对象语义；正文存在时必须是 JSON 对象。 / Empty body acts as an empty object; nonempty body must be a JSON object. */
        requestBody?: {
            content: {
                /**
                 * @example {
                 *       "credential_name": "example-provider",
                 *       "credential_info": {
                 *         "custom_llm_provider": "openai"
                 *       },
                 *       "credential_values": {
                 *         "api_base": "https://provider.example/v1",
                 *         "api_key": "example-not-a-secret"
                 *       }
                 *     }
                 */
                "application/json": components["schemas"]["CredentialInput"];
            };
        };
        responses: {
            /** @description 成功。 / Success. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Credential"];
                };
            };
            400: components["responses"]["Error400"];
            401: components["responses"]["Error401"];
            403: components["responses"]["Error403"];
            500: components["responses"]["Error500"];
            503: components["responses"]["Error503"];
        };
    };
}

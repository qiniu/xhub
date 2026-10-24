# 用户管理 API — 生成参考

由 OpenAPI 生成，不替换生产 handler。兼容及验证边界见 user-contract.md。

[OpenAPI](../../../contracts/users/openapi.json)

| 操作 | 行为 | HTTP 状态 |
| --- | --- | --- |
| `GET /user/list` | 按权限分页列出用户 | 200, 401, 403, 404, 500, 503 |
| `GET /user/info` | 读取自己或可见用户 | 200, 400, 401, 403, 404, 500, 503 |
| `GET /v2/user/info` | 用户详情兼容别名 | 200, 400, 401, 403, 404, 500, 503 |
| `GET /user/filter/ui` | 管理员选择器最多500项 | 200, 401, 403, 404, 500, 503 |
| `GET /user/available_users` | 管理员选择器兼容别名 | 200, 401, 403, 404, 500, 503 |
| `POST /user/new` | 创建用户并可绑定企业身份 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/update` | 按现有权限更新用户 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/bulk_update` | 原子修改用户预算和刷新日 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/delete` | 删除用户，不允许删除自己 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/set_password` | 按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/{user_id}/password` | 按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key | 200, 400, 401, 403, 404, 409, 500, 503 |

### GET /user/list

按权限分页列出用户

```json
[
  {
    "name": "page",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "按当前解析：无效/小于1的page回退1，page_size超出1..500回退100。"
  },
  {
    "name": "page_size",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "按当前解析：无效/小于1的page回退1，page_size超出1..500回退100。"
  },
  {
    "name": "search",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "user_email",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "user_ids",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "role",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "team",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "sso_user_ids",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "organization_ids",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "sort_by",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "sort_order",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  }
]
```


### GET /user/info

兼容GET对象正文中的user_id优先于query；建议使用query，普通用户只返回授权范围字段。

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  }
]
```


### GET /v2/user/info

兼容GET对象正文中的user_id优先于query；建议使用query，普通用户只返回授权范围字段。

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  }
]
```


### GET /user/filter/ui

管理员选择器最多500项

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "user_email",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  }
]
```


### GET /user/available_users

管理员选择器兼容别名

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  },
  {
    "name": "user_email",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "兼容查询字符串；授权及字段组合由服务执行。"
  }
]
```


### POST /user/new

创建用户并可绑定企业身份

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/CreateUser"
      }
    }
  }
}
```


### POST /user/update

按现有权限更新用户

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/UpdateUser"
      }
    }
  }
}
```


### POST /user/bulk_update

原子修改用户预算和刷新日

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/BulkBudgetInput"
      }
    }
  }
}
```


### POST /user/delete

删除用户，不允许删除自己

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/DeleteUser"
      }
    }
  }
}
```


### POST /user/set_password

按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/SetPasswordByBody"
      }
    }
  }
}
```


### POST /user/{user_id}/password

按现有权限重置密码并撤销旧会话；不返回密码，不撤销API Key

```json
[
  {
    "in": "path",
    "name": "user_id",
    "required": true,
    "schema": {
      "type": "string"
    }
  }
]
```

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/SetPassword"
      }
    }
  }
}
```


## 数据结构

### User

```json
{
  "type": "object",
  "properties": {
    "user_id": {
      "type": "string"
    },
    "user_email": {
      "type": "string"
    },
    "user_alias": {
      "type": "string"
    },
    "user_name": {
      "type": "string"
    },
    "user_role": {
      "type": "string"
    },
    "role": {
      "type": "string"
    },
    "status": {
      "type": "string"
    },
    "blocked": {
      "type": "boolean"
    },
    "max_budget": {
      "type": "number",
      "nullable": true,
      "format": "double"
    },
    "spend": {
      "type": "number",
      "format": "double"
    },
    "teams": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/UserTeam"
      }
    },
    "organizations": {
      "type": "array",
      "items": {},
      "description": "当前空占位，不是组织权限。"
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "当前空占位，不是可用模型列表。"
    },
    "created_at": {
      "type": "string",
      "format": "date-time"
    },
    "updated_at": {
      "type": "string",
      "format": "date-time"
    },
    "key_count": {
      "type": "integer",
      "description": "当前固定0占位，不是实际密钥数。"
    },
    "sso_user_id": {
      "type": "string",
      "nullable": true
    },
    "tpm_limit": {
      "type": "number",
      "nullable": true,
      "format": "double"
    },
    "rpm_limit": {
      "type": "number",
      "nullable": true,
      "format": "double"
    },
    "budget_reset_day": {
      "type": "integer",
      "minimum": 0,
      "maximum": 31
    },
    "budget_reset_at": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "budget_duration": {
      "type": "string",
      "nullable": true
    },
    "metadata": {
      "type": "object",
      "additionalProperties": true
    },
    "sso_connection_id": {
      "type": "string"
    },
    "sso_connection_name": {
      "type": "string"
    },
    "sso_external_id": {
      "type": "string"
    },
    "sso_binding_managed": {
      "type": "boolean"
    }
  },
  "required": [
    "user_id",
    "user_email",
    "user_alias",
    "user_name",
    "user_role",
    "role",
    "status",
    "blocked",
    "max_budget",
    "spend",
    "teams",
    "organizations",
    "models",
    "created_at",
    "updated_at",
    "key_count",
    "sso_user_id",
    "tpm_limit",
    "rpm_limit",
    "budget_reset_day",
    "budget_reset_at",
    "budget_duration",
    "metadata"
  ]
}
```

### UserTeam

```json
{
  "type": "object",
  "properties": {
    "team_id": {
      "type": "string"
    },
    "team_alias": {
      "type": "string"
    },
    "user_role": {
      "type": "string"
    },
    "organization_id": {
      "type": "string"
    }
  },
  "required": [
    "team_id",
    "team_alias",
    "user_role",
    "organization_id"
  ]
}
```

### UserDetail

```json
{
  "type": "object",
  "properties": {
    "user_info": {
      "$ref": "#/components/schemas/User"
    }
  },
  "required": [
    "user_info"
  ]
}
```

### LegacyBudget

```json
{
  "oneOf": [
    {
      "type": "number",
      "minimum": 0,
      "nullable": true,
      "format": "double"
    },
    {
      "type": "string"
    }
  ],
  "description": "单人兼容有限非负数字字符串；空字符串/null清空，省略保留（创建时不限额），0禁止消费；字符串的语义由服务校验。",
  "x-go-type": "json.RawMessage"
}
```

### LegacyRate

```json
{
  "oneOf": [
    {
      "type": "number",
      "minimum": 0,
      "nullable": true,
      "format": "double"
    },
    {
      "type": "string"
    }
  ],
  "description": "非负整数至2147483647，可用数字字符串，null/空字符串清空。",
  "x-go-type": "json.RawMessage"
}
```

### UserList

```json
{
  "type": "object",
  "properties": {
    "total": {
      "type": "integer"
    },
    "total_count": {
      "type": "integer"
    },
    "page": {
      "type": "integer"
    },
    "current_page": {
      "type": "integer"
    },
    "total_pages": {
      "type": "integer"
    },
    "page_size": {
      "type": "integer"
    },
    "size": {
      "type": "integer"
    },
    "users": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/User"
      }
    }
  },
  "required": [
    "total",
    "total_count",
    "page",
    "current_page",
    "total_pages",
    "page_size",
    "size",
    "users"
  ]
}
```

### UserPicker

```json
{
  "type": "object",
  "properties": {
    "users": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "user_id": {
            "type": "string"
          },
          "user_email": {
            "type": "string"
          },
          "user_alias": {
            "type": "string"
          }
        },
        "required": [
          "user_id",
          "user_email",
          "user_alias"
        ]
      }
    }
  },
  "required": [
    "users"
  ]
}
```

### CreateUser

```json
{
  "type": "object",
  "properties": {
    "max_budget": {
      "$ref": "#/components/schemas/LegacyBudget"
    },
    "max_budget_in_team": {
      "$ref": "#/components/schemas/LegacyBudget",
      "description": "兼容校验但不写入；不用于设置团队成员预算。"
    },
    "rpm_limit": {
      "$ref": "#/components/schemas/LegacyRate"
    },
    "tpm_limit": {
      "$ref": "#/components/schemas/LegacyRate"
    },
    "budget_reset_day": {
      "type": "integer",
      "minimum": 0,
      "maximum": 31,
      "description": "0关闭；1至31按北京时间00:00刷新，短月取末日；省略保留，null非法。"
    },
    "sso_connection_id": {
      "type": "string"
    },
    "sso_external_id": {
      "type": "string"
    },
    "user_email": {
      "type": "string",
      "description": "邮箱按既有规则去除首尾空白并转小写。"
    },
    "password": {
      "type": "string",
      "description": "8至72 UTF-8字节，不是字符数。",
      "writeOnly": true
    },
    "user_role": {
      "type": "string"
    },
    "user_alias": {
      "type": "string"
    },
    "user_name": {
      "type": "string"
    },
    "team_id": {
      "type": "string"
    },
    "team_role": {
      "type": "string"
    },
    "admin_organization_ids": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "organization_id": {
      "type": "string",
      "description": "设置组织管理员身份，不是普通组织归属。"
    }
  },
  "required": [
    "user_email",
    "password"
  ],
  "additionalProperties": true,
  "description": "平台管理员创建，不自动生成密钥；角色省略为user，兼容internal_user/proxy_admin；SSO两字段须同时提供；额外字段可能被忽略。",
  "x-go-type": "map[string]json.RawMessage"
}
```

### UpdateUser

```json
{
  "type": "object",
  "properties": {
    "max_budget": {
      "$ref": "#/components/schemas/LegacyBudget"
    },
    "max_budget_in_team": {
      "$ref": "#/components/schemas/LegacyBudget",
      "description": "兼容校验但不写入；不用于设置团队成员预算。"
    },
    "rpm_limit": {
      "$ref": "#/components/schemas/LegacyRate"
    },
    "tpm_limit": {
      "$ref": "#/components/schemas/LegacyRate"
    },
    "budget_reset_day": {
      "type": "integer",
      "minimum": 0,
      "maximum": 31,
      "description": "0关闭；1至31按北京时间00:00刷新，短月取末日；省略保留，null非法。"
    },
    "sso_connection_id": {
      "type": "string"
    },
    "sso_external_id": {
      "type": "string"
    },
    "user_id": {
      "type": "string"
    },
    "user_email": {
      "type": "string"
    },
    "user_alias": {
      "type": "string"
    },
    "user_role": {
      "type": "string"
    },
    "blocked": {
      "type": "boolean"
    },
    "password": {
      "type": "string",
      "writeOnly": true
    }
  },
  "required": [],
  "additionalProperties": true,
  "description": "完整单个JSON对象。目标省略为自己；普通用户只能改自己资料，管理字段不代表授予权限；预算写入单独授权。管理员密码与其他字段不是同一事务。",
  "x-go-type": "map[string]json.RawMessage"
}
```

### BudgetChanges

```json
{
  "anyOf": [
    {
      "type": "object",
      "properties": {
        "max_budget": {
          "type": "number",
          "nullable": true,
          "minimum": 0,
          "format": "double"
        },
        "budget_reset_day": {
          "type": "integer",
          "minimum": 0,
          "maximum": 31
        }
      },
      "required": [
        "max_budget"
      ],
      "additionalProperties": false
    },
    {
      "type": "object",
      "properties": {
        "max_budget": {
          "type": "number",
          "nullable": true,
          "minimum": 0,
          "format": "double"
        },
        "budget_reset_day": {
          "type": "integer",
          "minimum": 0,
          "maximum": 31
        }
      },
      "required": [
        "budget_reset_day"
      ],
      "additionalProperties": false
    }
  ],
  "x-go-type": "map[string]json.RawMessage"
}
```

### SelectedBudgetChange

```json
{
  "anyOf": [
    {
      "type": "object",
      "properties": {
        "max_budget": {
          "type": "number",
          "nullable": true,
          "minimum": 0,
          "format": "double"
        },
        "budget_reset_day": {
          "type": "integer",
          "minimum": 0,
          "maximum": 31
        },
        "user_id": {
          "type": "string",
          "minLength": 1
        }
      },
      "required": [
        "user_id",
        "max_budget"
      ],
      "additionalProperties": false
    },
    {
      "type": "object",
      "properties": {
        "max_budget": {
          "type": "number",
          "nullable": true,
          "minimum": 0,
          "format": "double"
        },
        "budget_reset_day": {
          "type": "integer",
          "minimum": 0,
          "maximum": 31
        },
        "user_id": {
          "type": "string",
          "minLength": 1
        }
      },
      "required": [
        "user_id",
        "budget_reset_day"
      ],
      "additionalProperties": false
    }
  ],
  "x-go-type": "map[string]json.RawMessage"
}
```

### SelectedBudgetBatch

```json
{
  "type": "object",
  "properties": {
    "all_users": {
      "type": "boolean",
      "enum": [
        false
      ]
    },
    "users": {
      "type": "array",
      "minItems": 1,
      "maxItems": 1000,
      "items": {
        "$ref": "#/components/schemas/SelectedBudgetChange"
      }
    }
  },
  "required": [
    "users"
  ],
  "additionalProperties": false
}
```

### AllBudgetBatch

```json
{
  "type": "object",
  "properties": {
    "all_users": {
      "type": "boolean",
      "enum": [
        true
      ]
    },
    "user_updates": {
      "$ref": "#/components/schemas/BudgetChanges"
    }
  },
  "required": [
    "all_users",
    "user_updates"
  ],
  "additionalProperties": false
}
```

### BulkBudgetInput

```json
{
  "oneOf": [
    {
      "$ref": "#/components/schemas/SelectedBudgetBatch"
    },
    {
      "$ref": "#/components/schemas/AllBudgetBatch"
    }
  ],
  "description": "平台管理员专用；最多1000用户、1MiB正文；ID不得重复，all_users包含禁用及管理员用户；原子失败不改变消费。",
  "x-go-type": "map[string]json.RawMessage"
}
```

### BulkBudgetResult

```json
{
  "type": "object",
  "properties": {
    "atomic": {
      "type": "boolean",
      "enum": [
        true
      ]
    },
    "batch_id": {
      "type": "string",
      "minLength": 1
    },
    "total_requested": {
      "type": "integer",
      "minimum": 0
    },
    "successful_updates": {
      "type": "integer",
      "minimum": 0
    },
    "failed_updates": {
      "type": "integer",
      "enum": [
        0
      ]
    },
    "results": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "user_id": {
            "type": "string"
          },
          "user_email": {
            "type": "string"
          },
          "success": {
            "type": "boolean",
            "enum": [
              true
            ]
          },
          "updated_user": {
            "$ref": "#/components/schemas/User"
          }
        },
        "required": [
          "user_id",
          "user_email",
          "success",
          "updated_user"
        ],
        "additionalProperties": false
      }
    }
  },
  "required": [
    "atomic",
    "batch_id",
    "total_requested",
    "successful_updates",
    "failed_updates",
    "results"
  ]
}
```

### DeleteUser

```json
{
  "type": "object",
  "properties": {
    "user_id": {
      "type": "string",
      "minLength": 1
    }
  },
  "required": [
    "user_id"
  ]
}
```

### DeletedUser

```json
{
  "type": "object",
  "properties": {
    "deleted": {
      "type": "boolean",
      "enum": [
        true
      ]
    }
  },
  "required": [
    "deleted"
  ]
}
```

### Error

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "$ref": "#/components/schemas/ErrorDetail"
    }
  },
  "description": "管理接口错误信封；HTTP 状态与 code 一致，不用成功对象代替失败。"
}
```

### Error401

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^401$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### Error403

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^403$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### Error500

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^500$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### Error503

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^503$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### Error400

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^400$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### Error404

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^404$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### ErrorDetail

```json
{
  "type": "object",
  "required": [
    "code",
    "message",
    "type",
    "param"
  ],
  "properties": {
    "code": {
      "type": "string",
      "pattern": "^[45][0-9]{2}$"
    },
    "message": {
      "type": "string"
    },
    "type": {
      "type": "string"
    },
    "param": {
      "nullable": true,
      "x-go-type": "json.RawMessage",
      "x-go-type-skip-optional-pointer": true,
      "description": "当前为 null。"
    }
  }
}
```

### Error409

```json
{
  "type": "object",
  "required": [
    "error"
  ],
  "properties": {
    "error": {
      "allOf": [
        {
          "$ref": "#/components/schemas/ErrorDetail"
        },
        {
          "type": "object",
          "required": [
            "code"
          ],
          "properties": {
            "code": {
              "type": "string",
              "pattern": "^409$"
            }
          }
        }
      ]
    }
  },
  "description": "完整错误字段在同一对象层合并，避免生成器覆盖嵌套 error。"
}
```

### SetPassword

```json
{
  "type": "object",
  "properties": {
    "user_id": {
      "type": "string"
    },
    "password": {
      "type": "string",
      "description": "8–72 UTF-8字节，由服务校验"
    }
  },
  "required": [
    "password"
  ],
  "additionalProperties": false
}
```

### SetPasswordByBody

```json
{
  "type": "object",
  "properties": {
    "user_id": {
      "type": "string"
    },
    "password": {
      "type": "string",
      "description": "8–72 UTF-8字节，由服务校验"
    }
  },
  "required": [
    "password",
    "user_id"
  ],
  "additionalProperties": false
}
```

### PasswordUpdated

```json
{
  "type": "object",
  "properties": {
    "user_id": {
      "type": "string"
    },
    "password_updated": {
      "type": "boolean",
      "enum": [
        true
      ]
    }
  },
  "required": [
    "user_id",
    "password_updated"
  ],
  "additionalProperties": false
}
```

# 密钥管理 API — 生成参考

由 OpenAPI 生成，不替换生产 handler。兼容及验证边界见 key-contract.md。

[OpenAPI](../../../contracts/keys/openapi.json)

| 操作 | 行为 | HTTP 状态 |
| --- | --- | --- |
| `POST /key/generate` | 创建个人Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /key/list` | 按权限列出Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /key/info` | 查询单个Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /v2/key/info` | 按正文查询单个Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/update` | 更新Key并保留省略字段 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/block` | 禁用Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/unblock` | 恢复Key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/delete` | 删除一个或多个Key | 200, 400, 401, 403, 404, 409, 500, 503 |

### POST /key/generate

仅创建返回明文；默认当前用户，团队由成员关系派生；空预算仍受个人与父级预算限制。支持契约为单个对象，旧handler还接受空正文/null，未声明兼容输入不承诺严格拒绝。

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/CreateKey"
      }
    }
  }
}
```


### GET /key/list

scope=personal仅登录用户本人，管理员也不例外；非法分页/排序返回400，空列表总页数为1。status/organization_id/expand等旧参数被忽略，不声明为有效筛选。

```json
[
  {
    "name": "scope",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string",
      "enum": [
        "personal"
      ]
    }
  },
  {
    "name": "page",
    "in": "query",
    "required": false,
    "schema": {
      "type": "integer",
      "minimum": 1,
      "maximum": 100000,
      "default": 1
    }
  },
  {
    "name": "size",
    "in": "query",
    "required": false,
    "schema": {
      "type": "integer",
      "minimum": 1,
      "maximum": 100,
      "default": 50
    }
  },
  {
    "name": "sort_by",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string",
      "enum": [
        "created_at",
        "updated_at",
        "key_alias",
        "token",
        "spend",
        "max_budget",
        "budget_remaining",
        "budget_utilization"
      ]
    }
  },
  {
    "name": "sort_order",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string",
      "enum": [
        "asc",
        "desc"
      ]
    }
  },
  {
    "name": "team_id",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string"
    }
  },
  {
    "name": "project_id",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string"
    }
  },
  {
    "name": "user_id",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string"
    }
  },
  {
    "name": "search",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string"
    }
  },
  {
    "name": "key_alias",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string"
    }
  },
  {
    "name": "key_hash",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string"
    },
    "description": "历史命名，实际按记录ID筛选 / Legacy name, actually filters by record ID."
  },
  {
    "name": "substring_matching",
    "in": "query",
    "required": false,
    "schema": {
      "type": "boolean"
    }
  }
]
```


### GET /key/info

查询单个Key

```json
[
  {
    "name": "key",
    "in": "query",
    "required": true,
    "schema": {
      "type": "string",
      "minLength": 1
    }
  },
  {
    "name": "scope",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string",
      "enum": [
        "personal"
      ]
    }
  }
]
```


### POST /v2/key/info

仅支持key，不支持keys数组；正文key覆盖查询参数，旧查询参数回退只记录兼容边界。

```json
[
  {
    "name": "scope",
    "in": "query",
    "required": false,
    "schema": {
      "type": "string",
      "enum": [
        "personal"
      ]
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
        "$ref": "#/components/schemas/KeySelector"
      }
    }
  }
}
```


### POST /key/update

返回Key对象，无data包装；不返回明文，不改变归属与历史消费。

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/UpdateKey"
      }
    }
  }
}
```


### POST /key/block

禁用Key

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/KeySelector"
      }
    }
  }
}
```


### POST /key/unblock

恢复Key

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/KeySelector"
      }
    }
  }
}
```


### POST /key/delete

先完成所有目标权限检查，再逐个删除；数据库中途失败可能部分成功，不是整批事务；重复删除404。

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/DeleteKeys"
      }
    }
  }
}
```


## 数据结构

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

### BudgetInput

```json
{
  "nullable": true,
  "anyOf": [
    {
      "type": "number",
      "format": "double",
      "minimum": 0
    },
    {
      "type": "string"
    }
  ],
  "x-go-type": "json.RawMessage",
  "description": "缺省保留（创建不设独立上限）；null/空串清空；0禁止此Key消费；字符串须为有限非负数"
}
```

### RateInput

```json
{
  "nullable": true,
  "anyOf": [
    {
      "type": "integer",
      "minimum": 0,
      "maximum": 2147483647
    },
    {
      "type": "string"
    }
  ],
  "x-go-type": "json.RawMessage",
  "description": "null/空串取消；字符串须为非负int32整数"
}
```

### CreateKey

```json
{
  "type": "object",
  "required": [],
  "properties": {
    "key_alias": {
      "type": "string"
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "nullable": true,
      "description": "空数组或全部模型哨兵表示继承，不扩大父级权限"
    },
    "max_budget": {
      "$ref": "#/components/schemas/BudgetInput"
    },
    "rpm_limit": {
      "$ref": "#/components/schemas/RateInput"
    },
    "tpm_limit": {
      "$ref": "#/components/schemas/RateInput"
    },
    "route_template_id": {
      "type": "string",
      "nullable": true,
      "description": "省略保留；null/空串清除并继承"
    },
    "duration": {
      "type": "string",
      "nullable": true,
      "description": "创建可省略；更新省略/null/空串保留旧期限，不清除；非空须有效正时长如30d"
    },
    "key_type": {
      "type": "string",
      "nullable": true,
      "enum": [
        "llm_api",
        "",
        null
      ]
    },
    "allowed_routes": {
      "type": "array",
      "nullable": true,
      "items": {
        "type": "string",
        "enum": [
          "llm_api_routes"
        ]
      }
    },
    "auto_rotate": {
      "type": "boolean",
      "nullable": true,
      "enum": [
        false,
        null
      ]
    },
    "rotation_interval": {
      "type": "string",
      "nullable": true,
      "enum": [
        "",
        null
      ]
    },
    "grace_period": {
      "type": "string",
      "nullable": true,
      "enum": [
        "",
        null
      ]
    },
    "key_name": {
      "type": "string"
    },
    "owner_type": {
      "type": "string",
      "enum": [
        "personal",
        ""
      ]
    },
    "user_id": {
      "type": "string"
    },
    "team_id": {
      "type": "string"
    },
    "project_id": {
      "type": "string"
    }
  },
  "x-go-type": "map[string]json.RawMessage"
}
```

### UpdateKey

```json
{
  "type": "object",
  "required": [
    "key"
  ],
  "properties": {
    "key": {
      "type": "string",
      "minLength": 1
    },
    "key_alias": {
      "type": "string"
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "nullable": true,
      "description": "空数组或全部模型哨兵表示继承，不扩大父级权限"
    },
    "max_budget": {
      "$ref": "#/components/schemas/BudgetInput"
    },
    "rpm_limit": {
      "$ref": "#/components/schemas/RateInput"
    },
    "tpm_limit": {
      "$ref": "#/components/schemas/RateInput"
    },
    "route_template_id": {
      "type": "string",
      "nullable": true,
      "description": "省略保留；null/空串清除并继承"
    },
    "duration": {
      "type": "string",
      "nullable": true,
      "description": "创建可省略；更新省略/null/空串保留旧期限，不清除；非空须有效正时长如30d"
    },
    "key_type": {
      "type": "string",
      "nullable": true,
      "enum": [
        "llm_api",
        "",
        null
      ]
    },
    "allowed_routes": {
      "type": "array",
      "nullable": true,
      "items": {
        "type": "string",
        "enum": [
          "llm_api_routes"
        ]
      }
    },
    "auto_rotate": {
      "type": "boolean",
      "nullable": true,
      "enum": [
        false,
        null
      ]
    },
    "rotation_interval": {
      "type": "string",
      "nullable": true,
      "enum": [
        "",
        null
      ]
    },
    "grace_period": {
      "type": "string",
      "nullable": true,
      "enum": [
        "",
        null
      ]
    }
  },
  "x-go-type": "map[string]json.RawMessage"
}
```

### KeySelector

```json
{
  "type": "object",
  "required": [
    "key"
  ],
  "properties": {
    "key": {
      "type": "string",
      "minLength": 1
    }
  }
}
```

### DeleteKeys

```json
{
  "anyOf": [
    {
      "type": "object",
      "required": [
        "keys"
      ],
      "properties": {
        "keys": {
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "string",
            "minLength": 1
          }
        },
        "key": {
          "type": "string"
        }
      }
    },
    {
      "type": "object",
      "required": [
        "key"
      ],
      "properties": {
        "key": {
          "type": "string",
          "minLength": 1
        },
        "keys": {
          "type": "array",
          "items": {
            "type": "string"
          }
        }
      }
    }
  ],
  "x-go-type": "map[string]json.RawMessage"
}
```

### KeyRecord

```json
{
  "type": "object",
  "required": [
    "token_id",
    "token",
    "key_name",
    "key_alias",
    "key_prefix",
    "owner_type",
    "status",
    "user_id",
    "team_id",
    "project_id",
    "created_by",
    "route_template_id",
    "key_type",
    "allowed_routes",
    "auto_rotate",
    "rotation_interval",
    "models",
    "max_budget",
    "spend",
    "rpm_limit",
    "tpm_limit",
    "blocked",
    "expires",
    "last_active",
    "created_at",
    "updated_at"
  ],
  "properties": {
    "token_id": {
      "type": "string"
    },
    "token": {
      "type": "string",
      "description": "创建为明文；普通列表/详情为遮罩；个人列表为记录ID，不可用其调用模型"
    },
    "key_name": {
      "type": "string"
    },
    "key_alias": {
      "type": "string"
    },
    "key_prefix": {
      "type": "string"
    },
    "owner_type": {
      "type": "string"
    },
    "status": {
      "type": "string"
    },
    "user_id": {
      "type": "string",
      "nullable": true
    },
    "team_id": {
      "type": "string",
      "nullable": true
    },
    "project_id": {
      "type": "string",
      "nullable": true
    },
    "created_by": {
      "type": "string",
      "nullable": true
    },
    "route_template_id": {
      "type": "string",
      "nullable": true
    },
    "key_type": {
      "type": "string",
      "enum": [
        "llm_api"
      ]
    },
    "allowed_routes": {
      "type": "array",
      "minItems": 1,
      "maxItems": 1,
      "items": {
        "type": "string",
        "enum": [
          "llm_api_routes"
        ]
      }
    },
    "auto_rotate": {
      "type": "boolean",
      "enum": [
        false
      ]
    },
    "rotation_interval": {
      "type": "string",
      "nullable": true,
      "enum": [
        null
      ]
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "max_budget": {
      "type": "number",
      "format": "double",
      "nullable": true
    },
    "spend": {
      "type": "number",
      "format": "double"
    },
    "rpm_limit": {
      "type": "integer",
      "nullable": true
    },
    "tpm_limit": {
      "type": "integer",
      "nullable": true
    },
    "blocked": {
      "type": "boolean"
    },
    "expires": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "last_active": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "created_at": {
      "type": "string",
      "format": "date-time",
      "nullable": false
    },
    "updated_at": {
      "type": "string",
      "format": "date-time",
      "nullable": false
    }
  },
  "additionalProperties": false
}
```

### CreatedKey

```json
{
  "type": "object",
  "required": [
    "token_id",
    "token",
    "key_name",
    "key_alias",
    "key_prefix",
    "owner_type",
    "status",
    "user_id",
    "team_id",
    "project_id",
    "created_by",
    "route_template_id",
    "key_type",
    "allowed_routes",
    "auto_rotate",
    "rotation_interval",
    "models",
    "max_budget",
    "spend",
    "rpm_limit",
    "tpm_limit",
    "blocked",
    "expires",
    "last_active",
    "created_at",
    "updated_at",
    "key"
  ],
  "properties": {
    "token_id": {
      "type": "string"
    },
    "token": {
      "type": "string",
      "description": "创建为明文；普通列表/详情为遮罩；个人列表为记录ID，不可用其调用模型"
    },
    "key_name": {
      "type": "string"
    },
    "key_alias": {
      "type": "string"
    },
    "key_prefix": {
      "type": "string"
    },
    "owner_type": {
      "type": "string"
    },
    "status": {
      "type": "string"
    },
    "user_id": {
      "type": "string",
      "nullable": true
    },
    "team_id": {
      "type": "string",
      "nullable": true
    },
    "project_id": {
      "type": "string",
      "nullable": true
    },
    "created_by": {
      "type": "string",
      "nullable": true
    },
    "route_template_id": {
      "type": "string",
      "nullable": true
    },
    "key_type": {
      "type": "string",
      "enum": [
        "llm_api"
      ]
    },
    "allowed_routes": {
      "type": "array",
      "minItems": 1,
      "maxItems": 1,
      "items": {
        "type": "string",
        "enum": [
          "llm_api_routes"
        ]
      }
    },
    "auto_rotate": {
      "type": "boolean",
      "enum": [
        false
      ]
    },
    "rotation_interval": {
      "type": "string",
      "nullable": true,
      "enum": [
        null
      ]
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "max_budget": {
      "type": "number",
      "format": "double",
      "nullable": true
    },
    "spend": {
      "type": "number",
      "format": "double"
    },
    "rpm_limit": {
      "type": "integer",
      "nullable": true
    },
    "tpm_limit": {
      "type": "integer",
      "nullable": true
    },
    "blocked": {
      "type": "boolean"
    },
    "expires": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "last_active": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "created_at": {
      "type": "string",
      "format": "date-time",
      "nullable": false
    },
    "updated_at": {
      "type": "string",
      "format": "date-time",
      "nullable": false
    },
    "key": {
      "type": "string"
    }
  },
  "additionalProperties": false
}
```

### BudgetReservation

```json
{
  "type": "object",
  "required": [
    "reservation_id",
    "call_id",
    "kind",
    "node_id",
    "key_id",
    "user_id",
    "team_id",
    "org_id",
    "project_id",
    "snapshot",
    "state",
    "reason",
    "amount",
    "started_at",
    "created_at"
  ],
  "properties": {
    "reservation_id": {
      "type": "string"
    },
    "call_id": {
      "type": "string"
    },
    "kind": {
      "type": "string"
    },
    "node_id": {
      "type": "string"
    },
    "key_id": {
      "type": "string"
    },
    "user_id": {
      "type": "string"
    },
    "team_id": {
      "type": "string"
    },
    "org_id": {
      "type": "string"
    },
    "project_id": {
      "type": "string"
    },
    "snapshot": {
      "type": "string"
    },
    "state": {
      "type": "string"
    },
    "reason": {
      "type": "string"
    },
    "amount": {
      "type": "number",
      "format": "double"
    },
    "started_at": {
      "type": "string",
      "format": "date-time"
    },
    "created_at": {
      "type": "string",
      "format": "date-time"
    }
  }
}
```

### KeyInfoRecord

```json
{
  "type": "object",
  "required": [
    "token_id",
    "token",
    "key_name",
    "key_alias",
    "key_prefix",
    "owner_type",
    "status",
    "user_id",
    "team_id",
    "project_id",
    "created_by",
    "route_template_id",
    "key_type",
    "allowed_routes",
    "auto_rotate",
    "rotation_interval",
    "models",
    "max_budget",
    "spend",
    "rpm_limit",
    "tpm_limit",
    "blocked",
    "expires",
    "last_active",
    "created_at",
    "updated_at",
    "budget_pending",
    "budget_reservations"
  ],
  "properties": {
    "token_id": {
      "type": "string"
    },
    "token": {
      "type": "string",
      "description": "创建为明文；普通列表/详情为遮罩；个人列表为记录ID，不可用其调用模型"
    },
    "key_name": {
      "type": "string"
    },
    "key_alias": {
      "type": "string"
    },
    "key_prefix": {
      "type": "string"
    },
    "owner_type": {
      "type": "string"
    },
    "status": {
      "type": "string"
    },
    "user_id": {
      "type": "string",
      "nullable": true
    },
    "team_id": {
      "type": "string",
      "nullable": true
    },
    "project_id": {
      "type": "string",
      "nullable": true
    },
    "created_by": {
      "type": "string",
      "nullable": true
    },
    "route_template_id": {
      "type": "string",
      "nullable": true
    },
    "key_type": {
      "type": "string",
      "enum": [
        "llm_api"
      ]
    },
    "allowed_routes": {
      "type": "array",
      "minItems": 1,
      "maxItems": 1,
      "items": {
        "type": "string",
        "enum": [
          "llm_api_routes"
        ]
      }
    },
    "auto_rotate": {
      "type": "boolean",
      "enum": [
        false
      ]
    },
    "rotation_interval": {
      "type": "string",
      "nullable": true,
      "enum": [
        null
      ]
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "max_budget": {
      "type": "number",
      "format": "double",
      "nullable": true
    },
    "spend": {
      "type": "number",
      "format": "double"
    },
    "rpm_limit": {
      "type": "integer",
      "nullable": true
    },
    "tpm_limit": {
      "type": "integer",
      "nullable": true
    },
    "blocked": {
      "type": "boolean"
    },
    "expires": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "last_active": {
      "type": "string",
      "format": "date-time",
      "nullable": true
    },
    "created_at": {
      "type": "string",
      "format": "date-time",
      "nullable": false
    },
    "updated_at": {
      "type": "string",
      "format": "date-time",
      "nullable": false
    },
    "budget_pending": {
      "type": "number",
      "format": "double"
    },
    "budget_reservations": {
      "type": "array",
      "nullable": true,
      "items": {
        "$ref": "#/components/schemas/BudgetReservation"
      }
    }
  },
  "additionalProperties": false
}
```

### KeyInfo

```json
{
  "type": "object",
  "required": [
    "info"
  ],
  "properties": {
    "info": {
      "$ref": "#/components/schemas/KeyInfoRecord"
    }
  }
}
```

### KeyList

```json
{
  "type": "object",
  "required": [
    "keys",
    "total_count",
    "current_page",
    "total_pages",
    "size"
  ],
  "properties": {
    "keys": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/KeyRecord"
      }
    },
    "total_count": {
      "type": "integer",
      "minimum": 0
    },
    "current_page": {
      "type": "integer",
      "minimum": 1
    },
    "total_pages": {
      "type": "integer",
      "minimum": 1
    },
    "size": {
      "type": "integer",
      "minimum": 1,
      "maximum": 100
    }
  }
}
```

### DeletedKeys

```json
{
  "type": "object",
  "required": [
    "deleted"
  ],
  "properties": {
    "deleted": {
      "type": "integer",
      "minimum": 1
    }
  }
}
```

### BlockedKey

```json
{
  "type": "object",
  "required": [
    "blocked"
  ],
  "properties": {
    "blocked": {
      "type": "boolean",
      "enum": [
        true
      ]
    }
  }
}
```

### UnblockedKey

```json
{
  "type": "object",
  "required": [
    "blocked"
  ],
  "properties": {
    "blocked": {
      "type": "boolean",
      "enum": [
        false
      ]
    }
  }
}
```

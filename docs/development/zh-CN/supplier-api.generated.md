# 供应商管理 API — 生成参考

由试点 OpenAPI 源生成，不替换生产路由或客户端。兼容及验证边界见 supplier-contract-pilot.md。

[OpenAPI](../../../contracts/suppliers/openapi.json)

| 操作 | 行为 | HTTP 状态 |
| --- | --- | --- |
| `GET /credentials` | 列出供应商（两个等价列表字段）。 | 200, 400, 401, 403, 500, 503 |
| `POST /credentials` | 保存供应商，同名创建覆盖已有配置。 | 200, 400, 401, 403, 500, 503 |
| `GET /credentials/by_name/{credential_name}` | 读取供应商，敏感字符串脱敏。 | 200, 400, 401, 403, 404, 500, 503 |
| `PATCH /credentials/{credential_name}` | 合并补丁；不存在则创建。 | 200, 400, 401, 403, 500, 503 |
| `DELETE /credentials/{credential_name}` | 删除供应商；重复删除返回 deleted=0。 | 200, 400, 401, 403, 500, 503 |

## 数据结构

### CredentialInput

```json
{
  "type": "object",
  "description": "创建/更新 JSON 对象；没有必填业务字段，扩展字段保留，password 顶层字段忽略，jsonrpc 顶层字段拒绝。",
  "properties": {
    "credential_name": {
      "nullable": true,
      "description": "供应商名；创建缺省或非字符串时生成 ID。"
    },
    "credential_info": {
      "nullable": true,
      "description": "供应商元数据，历史接口允许任意 JSON。"
    },
    "credential_values": {
      "nullable": true,
      "description": "凭据字段；对象内敏感字符串读取时脱敏，创建响应省略对象。PATCH 忽略空值或含 ** 的敏感字段，非对象补丁忽略。"
    },
    "created_at": {
      "nullable": true,
      "description": "创建时间；历史接口允许调用者覆盖。"
    }
  },
  "additionalProperties": true,
  "x-go-type": "map[string]json.RawMessage"
}
```

### Credential

```json
{
  "type": "object",
  "required": [
    "id",
    "credential_name",
    "credential_info"
  ],
  "properties": {
    "credential_info": {
      "nullable": true,
      "description": "供应商元数据，历史接口允许任意 JSON。"
    },
    "credential_values": {
      "nullable": true,
      "description": "凭据字段；对象内敏感字符串读取时脱敏，创建响应省略对象。PATCH 忽略空值或含 ** 的敏感字段，非对象补丁忽略。"
    },
    "created_at": {
      "nullable": true,
      "description": "创建时间；历史接口允许调用者覆盖。"
    },
    "id": {
      "type": "string"
    },
    "credential_name": {
      "type": "string"
    }
  },
  "additionalProperties": true,
  "x-go-type": "map[string]json.RawMessage",
  "description": "供应商读取或保存结果；管理接口返回，Go 原始 JSON 类型保留扩展字段及 null。"
}
```

### CredentialList

```json
{
  "type": "object",
  "required": [
    "object",
    "data",
    "credentials"
  ],
  "properties": {
    "object": {
      "type": "string",
      "enum": [
        "list"
      ]
    },
    "data": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/Credential"
      }
    },
    "credentials": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/Credential"
      }
    }
  },
  "description": "供应商列表响应；data 与 credentials 是同一列表的兼容别名。"
}
```

### Deletion

```json
{
  "type": "object",
  "required": [
    "id",
    "credential_name",
    "deleted"
  ],
  "properties": {
    "id": {
      "type": "string"
    },
    "credential_name": {
      "type": "string"
    },
    "deleted": {
      "type": "integer",
      "minimum": 0
    }
  },
  "description": "删除结果；deleted 为实际删除条数，重复删除可为零。"
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
  },
  "description": "管理接口错误信封；HTTP 状态与 code 一致，不用成功对象代替失败。"
}
```

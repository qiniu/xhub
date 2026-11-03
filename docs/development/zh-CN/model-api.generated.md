# 模型与端点 API — 生成参考

由 OpenAPI 生成，不替换生产 handler。验证边界见 model-contract.md。

[OpenAPI](../../../contracts/models/openapi.json)

| 操作 | 行为 | HTTP 状态 |
| --- | --- | --- |
| `GET /v2/model/info` | 读取部署列表 | 200, 401, 403 |
| `GET /model/groups` | 读取公开模型分组 | 200, 401, 403, 500, 503 |
| `POST /model/new` | 创建数据库部署 | 200, 400, 401, 403, 409, 500 |
| `POST /model/update` | 合并部署补丁 | 200, 400, 401, 403, 409, 500 |
| `PATCH /model/{model_id}/update` | 按路径更新部署 | 200, 400, 401, 403, 409, 500 |
| `POST /model/delete` | 删除部署 | 200, 400, 401, 403, 409, 500 |
| `POST /model/disable` | 停用部署 | 200, 400, 401, 403, 500 |
| `POST /model/enable` | 启用部署 | 200, 400, 401, 403, 500 |
| `POST /health/test_connection` | 探测保存的部署 | 200, 400, 401, 403, 404 |

### GET /v2/model/info

仅平台管理员；未知ID为空列表；其他旧筛选参数被忽略

```json
[
  {
    "in": "query",
    "name": "modelId",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  },
  {
    "in": "query",
    "name": "search",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  },
  {
    "in": "query",
    "name": "size",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  }
]
```


### GET /model/groups

读取公开模型分组

```json
[
  {
    "in": "query",
    "name": "search",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  },
  {
    "in": "query",
    "name": "size",
    "schema": {
      "type": "string"
    },
    "description": "保留既有字符串解析"
  }
]
```


### POST /model/new

声明已支持输入；附加跨字段校验由真实服务执行，未安装运行时严格校验

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/CreateDeployment"
      }
    }
  }
}
```


### POST /model/update

省略保持；星号遮罩不覆盖密钥；ID从model_info.id或id读取

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/UpdateDeployment"
      }
    }
  }
}
```


### PATCH /model/{model_id}/update

按路径更新部署

```json
[
  {
    "in": "path",
    "name": "model_id",
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
        "$ref": "#/components/schemas/UpdateDeployment"
      }
    }
  }
}
```


### POST /model/delete

删除部署

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/DeleteSelector"
      }
    }
  }
}
```


### POST /model/disable

停用部署

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/DeploymentSelector"
      }
    }
  }
}
```


### POST /model/enable

启用部署

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/DeploymentSelector"
      }
    }
  }
}
```


### POST /health/test_connection

只使用保存的地址和凭据；失败也可为HTTP200，必须检查healthy；不经过预算和记账

```json
{
  "required": true,
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/ConnectionRequest"
      }
    }
  }
}
```


## 数据结构

### Parameters

```json
{
  "type": "object",
  "properties": {
    "model": {
      "type": "string"
    },
    "custom_llm_provider": {
      "type": "string"
    },
    "litellm_credential_name": {
      "type": "string"
    },
    "api_base": {
      "type": "string"
    },
    "api_key": {
      "type": "string"
    },
    "input_cost_per_token": {
      "anyOf": [
        {
          "type": "number",
          "format": "double",
          "minimum": 0
        },
        {
          "type": "string",
          "nullable": true
        }
      ],
      "description": "兼容有限非负数字字符串或null；字符串数值由服务检查"
    },
    "output_cost_per_token": {
      "anyOf": [
        {
          "type": "number",
          "format": "double",
          "minimum": 0
        },
        {
          "type": "string",
          "nullable": true
        }
      ],
      "description": "兼容有限非负数字字符串或null；字符串数值由服务检查"
    }
  },
  "required": [],
  "additionalProperties": true,
  "description": "上游参数允许扩展；读取时敏感顶层字符串打码，不能认为所有嵌套字段均脱敏",
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
}
```

### ModelInfo

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string"
    },
    "transport": {
      "type": "string"
    },
    "endpoint_types": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "pricing_source": {
      "type": "string"
    },
    "base_model": {
      "type": "string"
    },
    "disabled": {
      "type": "boolean"
    },
    "db_model": {
      "type": "boolean"
    },
    "unavailable_reason": {
      "type": "string"
    }
  },
  "required": [],
  "additionalProperties": true,
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
}
```

### Deployment

```json
{
  "type": "object",
  "properties": {
    "model_name": {
      "type": "string"
    },
    "litellm_params": {
      "$ref": "#/components/schemas/Parameters"
    },
    "model_info": {
      "$ref": "#/components/schemas/PublicModelInfo"
    }
  },
  "required": [
    "model_name",
    "litellm_params",
    "model_info"
  ],
  "additionalProperties": false
}
```

### DeploymentRow

```json
{
  "type": "object",
  "properties": {
    "model_name": {
      "type": "string"
    },
    "litellm_params": {
      "$ref": "#/components/schemas/Parameters"
    },
    "model_info": {
      "$ref": "#/components/schemas/PublicModelInfo"
    },
    "id": {
      "type": "string"
    },
    "db_model": {
      "type": "boolean"
    }
  },
  "required": [
    "model_name",
    "litellm_params",
    "model_info",
    "id",
    "db_model"
  ],
  "additionalProperties": false
}
```

### DeletedDeployment

```json
{
  "type": "object",
  "properties": {
    "model_name": {
      "type": "string"
    },
    "litellm_params": {
      "$ref": "#/components/schemas/Parameters"
    },
    "model_info": {
      "$ref": "#/components/schemas/PublicModelInfo"
    },
    "id": {
      "type": "string"
    },
    "deleted": {
      "type": "boolean",
      "enum": [
        true
      ]
    }
  },
  "required": [
    "model_name",
    "litellm_params",
    "model_info",
    "id",
    "deleted"
  ],
  "additionalProperties": false
}
```

### DeploymentList

```json
{
  "type": "object",
  "properties": {
    "data": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/DeploymentRow"
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
      "minimum": 1
    }
  },
  "required": [
    "data",
    "total_count",
    "current_page",
    "total_pages",
    "size"
  ],
  "additionalProperties": false
}
```

### DefaultWeights

```json
{
  "type": "object",
  "properties": {
    "allocations": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "deployment_id": {
            "type": "string"
          },
          "weight": {
            "type": "number",
            "format": "double"
          }
        },
        "required": [
          "deployment_id",
          "weight"
        ],
        "additionalProperties": false
      },
      "nullable": true
    }
  },
  "required": [
    "allocations"
  ],
  "additionalProperties": false
}
```

### FallbackPolicy

```json
{
  "type": "object",
  "properties": {
    "fallbacks": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "nullable": true
    },
    "context_window_fallbacks": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "nullable": true
    },
    "content_policy_fallbacks": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "nullable": true
    }
  },
  "required": [
    "fallbacks",
    "context_window_fallbacks",
    "content_policy_fallbacks"
  ],
  "additionalProperties": false
}
```

### ModelGroup

```json
{
  "type": "object",
  "properties": {
    "model_name": {
      "type": "string"
    },
    "deployments": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/DeploymentRow"
      }
    },
    "default_weights": {
      "$ref": "#/components/schemas/DefaultWeights"
    },
    "fallback_policy": {
      "$ref": "#/components/schemas/FallbackPolicy"
    }
  },
  "required": [
    "model_name",
    "deployments",
    "default_weights",
    "fallback_policy"
  ],
  "additionalProperties": false
}
```

### GroupList

```json
{
  "type": "object",
  "properties": {
    "data": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/ModelGroup"
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
    }
  },
  "required": [
    "data",
    "total_count",
    "current_page",
    "total_pages"
  ],
  "additionalProperties": false
}
```

### CreateDeployment

```json
{
  "type": "object",
  "properties": {
    "model_name": {
      "type": "string"
    },
    "litellm_params": {
      "$ref": "#/components/schemas/Parameters"
    },
    "model_info": {
      "$ref": "#/components/schemas/ModelInfo"
    }
  },
  "required": [
    "model_name",
    "litellm_params",
    "model_info"
  ],
  "additionalProperties": true,
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
}
```

### UpdateDeployment

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string"
    },
    "model_name": {
      "type": "string"
    },
    "litellm_params": {
      "$ref": "#/components/schemas/Parameters"
    },
    "model_info": {
      "$ref": "#/components/schemas/ModelInfo"
    },
    "disabled": {
      "type": "boolean"
    }
  },
  "required": [],
  "additionalProperties": true,
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
}
```

### DeploymentSelector

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string"
    },
    "model": {
      "type": "string"
    },
    "model_name": {
      "type": "string"
    }
  },
  "required": [],
  "additionalProperties": true,
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  },
  "anyOf": [
    {
      "type": "object",
      "properties": {
        "id": {
          "type": "string"
        }
      },
      "required": [
        "id"
      ]
    },
    {
      "type": "object",
      "properties": {
        "model": {
          "type": "string"
        }
      },
      "required": [
        "model"
      ]
    },
    {
      "type": "object",
      "properties": {
        "model_name": {
          "type": "string"
        }
      },
      "required": [
        "model_name"
      ]
    }
  ]
}
```

### ConnectionRequest

```json
{
  "type": "object",
  "properties": {
    "model_info": {
      "type": "object",
      "properties": {
        "id": {
          "type": "string"
        }
      },
      "required": [
        "id"
      ],
      "additionalProperties": true
    },
    "litellm_params": {
      "$ref": "#/components/schemas/Parameters"
    },
    "model": {
      "type": "string"
    },
    "mode": {
      "type": "string"
    }
  },
  "required": [
    "model_info"
  ],
  "additionalProperties": true,
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
}
```

### ConnectionSuccess

```json
{
  "type": "object",
  "properties": {
    "status": {
      "type": "string",
      "enum": [
        "success"
      ]
    },
    "healthy": {
      "type": "boolean",
      "enum": [
        true
      ]
    },
    "error": {
      "type": "string",
      "nullable": true,
      "enum": [
        null
      ],
      "x-go-type": "rawjson.RawMessage",
      "x-go-type-import": {
        "path": "encoding/json",
        "name": "rawjson"
      }
    },
    "model": {
      "type": "string"
    },
    "mode": {
      "type": "string"
    }
  },
  "required": [
    "status",
    "healthy",
    "error",
    "model",
    "mode"
  ],
  "additionalProperties": false
}
```

### ConnectionFailure

```json
{
  "type": "object",
  "properties": {
    "status": {
      "type": "string",
      "enum": [
        "error"
      ]
    },
    "healthy": {
      "type": "boolean",
      "enum": [
        false
      ]
    },
    "error": {
      "type": "string"
    },
    "message": {
      "type": "string"
    },
    "result": {
      "type": "object",
      "properties": {
        "error": {
          "type": "string"
        }
      },
      "required": [
        "error"
      ],
      "additionalProperties": false
    },
    "model": {
      "type": "string"
    },
    "mode": {
      "type": "string"
    }
  },
  "required": [
    "status",
    "healthy",
    "error",
    "message",
    "result",
    "model",
    "mode"
  ],
  "additionalProperties": false
}
```

### ConnectionResult

```json
{
  "oneOf": [
    {
      "$ref": "#/components/schemas/ConnectionSuccess"
    },
    {
      "$ref": "#/components/schemas/ConnectionFailure"
    }
  ],
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
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

### PublicModelInfo

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string"
    },
    "transport": {
      "type": "string"
    },
    "endpoint_types": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "pricing_source": {
      "type": "string"
    },
    "base_model": {
      "type": "string"
    },
    "disabled": {
      "type": "boolean"
    },
    "db_model": {
      "type": "boolean"
    },
    "unavailable_reason": {
      "type": "string"
    }
  },
  "required": [
    "id",
    "disabled",
    "db_model"
  ],
  "additionalProperties": true,
  "description": "读取时保证稳定ID和状态；其他扩展保持原值",
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  }
}
```

### DeleteSelector

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string"
    },
    "model_name": {
      "type": "string"
    }
  },
  "required": [],
  "additionalProperties": true,
  "x-go-type": "rawjson.RawMessage",
  "x-go-type-import": {
    "path": "encoding/json",
    "name": "rawjson"
  },
  "anyOf": [
    {
      "type": "object",
      "properties": {
        "id": {
          "type": "string"
        }
      },
      "required": [
        "id"
      ]
    },
    {
      "type": "object",
      "properties": {
        "model_name": {
          "type": "string"
        }
      },
      "required": [
        "model_name"
      ]
    }
  ]
}
```

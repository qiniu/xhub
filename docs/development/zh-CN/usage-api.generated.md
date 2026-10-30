# 日志与用量 API — 生成参考

由 OpenAPI 生成，保留既有权限与计费。范围见 core-contract-completion.md。

[OpenAPI](../../../contracts/usage/openapi.json)

| 操作 | 行为 | HTTP 状态 |
| --- | --- | --- |
| `GET /spend/logs/v2` | 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /spend/logs/ui` | 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /spend/logs/session/ui` | 权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /spend/logs/ui/{request_id}` | 范围外与不存在均404；管理员读取他人详情记审计；正文缺失可为空，非JSON上游响应为字符串；并非所有错误均有账单 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /user/daily/activity` | 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /user/daily/activity/aggregated` | 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /team/daily/activity` | 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /team/daily/activity/aggregated` | 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /organization/daily/activity` | 授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0 | 200, 400, 401, 403, 404, 409, 500, 503 |

### GET /spend/logs/v2

权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    },
    "description": "支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天"
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    },
    "description": "支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天"
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page_size",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "session_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "request_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "search",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "error_code",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "error_message",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "sort_by",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "sort_order",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "status_filter",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "group_by_session",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /spend/logs/ui

权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    },
    "description": "支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天"
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    },
    "description": "支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天"
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page_size",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "session_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "request_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "search",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "error_code",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "error_message",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "sort_by",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "sort_order",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "status_filter",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "group_by_session",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /spend/logs/session/ui

权限范围内日志，page无效回退1，page_size超出1..200回退50；日期按UTC含结束日；完整时间精确过滤，非法或倒置窗口返回400；会话聚合先分组后分页；未声明筛选不保证有效

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    },
    "description": "支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天"
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    },
    "description": "支持 YYYY-MM-DD（UTC整天）、YYYY-MM-DD HH:mm:ss（UTC）或 RFC3339；完整结束时间不延长一天"
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page_size",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "session_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "request_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "search",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "error_code",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "error_message",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "sort_by",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "sort_order",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "status_filter",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "group_by_session",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /spend/logs/ui/{request_id}

范围外与不存在均404；管理员读取他人详情记审计；正文缺失可为空，非JSON上游响应为字符串；并非所有错误均有账单

```json
[
  {
    "in": "path",
    "name": "request_id",
    "required": true,
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /user/daily/activity

授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "timezone",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "exclude_team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "organization_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /user/daily/activity/aggregated

授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "timezone",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "exclude_team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "organization_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /team/daily/activity

授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "timezone",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "exclude_team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "organization_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /team/daily/activity/aggregated

授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "timezone",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "exclude_team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "organization_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /organization/daily/activity

授权后按天汇总；分页合计仅当前页，aggregated合计全范围；无效日期保留开放边界；缓存未知不填0

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "page",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "timezone",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "user_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_id",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "exclude_team_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "organization_ids",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "api_key",
    "schema": {
      "type": "string"
    }
  },
  {
    "in": "query",
    "name": "model",
    "schema": {
      "type": "string"
    }
  }
]
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

### LogRecord

```json
{
  "type": "object",
  "properties": {
    "request_id": {
      "type": "string"
    },
    "api_key": {
      "type": "string"
    },
    "team_id": {
      "type": "string"
    },
    "model": {
      "type": "string"
    },
    "model_id": {
      "type": "string"
    },
    "call_type": {
      "type": "string"
    },
    "reasoning_effort": {
      "type": "string"
    },
    "startTime": {
      "type": "string"
    },
    "endTime": {
      "type": "string"
    },
    "inbound_endpoint": {
      "type": "string"
    },
    "outbound_endpoint": {
      "type": "string"
    },
    "user": {
      "type": "string"
    },
    "cache_hit": {
      "type": "string"
    },
    "cache_key": {
      "type": "string"
    },
    "session_id": {
      "type": "string"
    },
    "session_source": {
      "type": "string"
    },
    "status": {
      "type": "string"
    },
    "owner_type": {
      "type": "string"
    },
    "project_id": {
      "type": "string"
    },
    "organization_id": {
      "type": "string"
    },
    "custom_llm_provider": {
      "type": "string"
    },
    "credential_name": {
      "type": "string"
    },
    "api_base": {
      "type": "string"
    },
    "label": {
      "type": "string"
    },
    "spend": {
      "type": "number"
    },
    "total_tokens": {
      "type": "number"
    },
    "prompt_tokens": {
      "type": "number"
    },
    "completion_tokens": {
      "type": "number"
    },
    "request_duration_ms": {
      "type": "number"
    },
    "streamed": {
      "type": "boolean"
    },
    "ttft_ms": {
      "type": "number",
      "nullable": true
    },
    "tps": {
      "type": "number",
      "nullable": true
    },
    "messages": {
      "x-go-type": "rawjson.RawMessage",
      "x-go-type-import": {
        "name": "rawjson",
        "path": "encoding/json"
      },
      "description": "保留任意JSON类型、未知字段与null"
    },
    "response": {
      "x-go-type": "rawjson.RawMessage",
      "x-go-type-import": {
        "name": "rawjson",
        "path": "encoding/json"
      },
      "description": "保留任意JSON类型、未知字段与null"
    },
    "metadata": {
      "type": "object",
      "additionalProperties": true,
      "x-go-type": "rawjson.RawMessage",
      "x-go-type-import": {
        "name": "rawjson",
        "path": "encoding/json"
      }
    },
    "completionStartTime": {
      "type": "string"
    },
    "cache_read_input_tokens": {
      "type": "number"
    },
    "error": {
      "type": "string"
    },
    "proxy_server_request": {
      "x-go-type": "rawjson.RawMessage",
      "x-go-type-import": {
        "name": "rawjson",
        "path": "encoding/json"
      },
      "description": "保留任意JSON类型、未知字段与null"
    },
    "session_total_count": {
      "type": "integer",
      "minimum": 0
    },
    "session_llm_count": {
      "type": "integer",
      "minimum": 0
    },
    "session_spend": {
      "type": "number"
    },
    "session_total_tokens": {
      "type": "number"
    }
  },
  "required": [
    "request_id",
    "api_key",
    "team_id",
    "model",
    "model_id",
    "call_type",
    "reasoning_effort",
    "startTime",
    "endTime",
    "inbound_endpoint",
    "outbound_endpoint",
    "user",
    "cache_hit",
    "cache_key",
    "session_id",
    "session_source",
    "status",
    "owner_type",
    "project_id",
    "organization_id",
    "custom_llm_provider",
    "credential_name",
    "api_base",
    "label",
    "spend",
    "total_tokens",
    "prompt_tokens",
    "completion_tokens",
    "request_duration_ms",
    "streamed",
    "ttft_ms",
    "tps",
    "messages",
    "response",
    "metadata"
  ],
  "additionalProperties": {
    "x-go-type": "rawjson.RawMessage",
    "x-go-type-import": {
      "name": "rawjson",
      "path": "encoding/json"
    },
    "description": "保留任意JSON类型、未知字段与null"
  }
}
```

### LogPage

```json
{
  "type": "object",
  "properties": {
    "data": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/LogRecord"
      }
    },
    "total": {
      "type": "integer",
      "minimum": 0
    },
    "page": {
      "type": "integer",
      "minimum": 0
    },
    "page_size": {
      "type": "integer",
      "minimum": 0
    },
    "total_pages": {
      "type": "integer",
      "minimum": 0
    }
  },
  "required": [
    "data",
    "total",
    "page",
    "page_size",
    "total_pages"
  ],
  "additionalProperties": false
}
```

### UsageMetrics

```json
{
  "type": "object",
  "properties": {
    "spend": {
      "type": "number"
    },
    "flat_cost": {
      "type": "number"
    },
    "prompt_tokens": {
      "type": "number"
    },
    "completion_tokens": {
      "type": "number"
    },
    "total_tokens": {
      "type": "number"
    },
    "api_requests": {
      "type": "number"
    },
    "successful_requests": {
      "type": "number"
    },
    "failed_requests": {
      "type": "number"
    },
    "compression_saved_tokens": {
      "type": "number"
    },
    "compression_savings_spend": {
      "type": "number"
    },
    "prompt_caching_savings_spend": {
      "type": "number"
    },
    "gateway_injected_caching_savings_spend": {
      "type": "number"
    },
    "autorouter_savings_spend": {
      "type": "number"
    },
    "cache_read_input_tokens": {
      "type": "number"
    }
  },
  "required": [
    "spend",
    "flat_cost",
    "prompt_tokens",
    "completion_tokens",
    "total_tokens",
    "api_requests",
    "successful_requests",
    "failed_requests",
    "compression_saved_tokens",
    "compression_savings_spend",
    "prompt_caching_savings_spend",
    "gateway_injected_caching_savings_spend",
    "autorouter_savings_spend"
  ],
  "additionalProperties": false
}
```

### UsageBucket

```json
{
  "type": "object",
  "properties": {
    "metrics": {
      "$ref": "#/components/schemas/UsageMetrics"
    },
    "metadata": {
      "type": "object",
      "additionalProperties": true
    },
    "api_key_breakdown": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageKeyBucket"
      }
    }
  },
  "required": [
    "metrics",
    "metadata",
    "api_key_breakdown"
  ],
  "additionalProperties": false
}
```

### UsageBreakdown

```json
{
  "type": "object",
  "properties": {
    "models": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageBucket"
      }
    },
    "model_groups": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageBucket"
      }
    },
    "providers": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageBucket"
      }
    },
    "api_keys": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageKeyBucket"
      }
    },
    "endpoints": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageBucket"
      }
    },
    "mcp_servers": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageBucket"
      }
    },
    "entities": {
      "type": "object",
      "additionalProperties": {
        "$ref": "#/components/schemas/UsageBucket"
      }
    }
  },
  "required": [
    "models",
    "model_groups",
    "providers",
    "api_keys",
    "endpoints",
    "mcp_servers",
    "entities"
  ],
  "additionalProperties": false
}
```

### UsageDay

```json
{
  "type": "object",
  "properties": {
    "date": {
      "type": "string"
    },
    "metrics": {
      "$ref": "#/components/schemas/UsageMetrics"
    },
    "breakdown": {
      "$ref": "#/components/schemas/UsageBreakdown"
    }
  },
  "required": [
    "date",
    "metrics",
    "breakdown"
  ],
  "additionalProperties": false
}
```

### UsageMetadata

```json
{
  "type": "object",
  "properties": {
    "total_spend": {
      "type": "number"
    },
    "total_flat_cost": {
      "type": "number"
    },
    "total_prompt_tokens": {
      "type": "number"
    },
    "total_completion_tokens": {
      "type": "number"
    },
    "total_api_requests": {
      "type": "number"
    },
    "total_successful_requests": {
      "type": "number"
    },
    "total_failed_requests": {
      "type": "number"
    },
    "total_compression_saved_tokens": {
      "type": "number"
    },
    "total_compression_savings_spend": {
      "type": "number"
    },
    "total_prompt_caching_savings_spend": {
      "type": "number"
    },
    "total_gateway_injected_caching_savings_spend": {
      "type": "number"
    },
    "total_autorouter_savings_spend": {
      "type": "number"
    },
    "total_cache_read_input_tokens": {
      "type": "number"
    },
    "total_pages": {
      "type": "integer",
      "minimum": 0
    },
    "page": {
      "type": "integer",
      "minimum": 0
    },
    "has_more": {
      "type": "boolean"
    },
    "total_tokens": {
      "type": "number"
    }
  },
  "required": [
    "total_spend",
    "total_flat_cost",
    "total_prompt_tokens",
    "total_completion_tokens",
    "total_tokens",
    "total_api_requests",
    "total_successful_requests",
    "total_failed_requests",
    "total_compression_saved_tokens",
    "total_compression_savings_spend",
    "total_prompt_caching_savings_spend",
    "total_gateway_injected_caching_savings_spend",
    "total_autorouter_savings_spend",
    "total_pages",
    "page",
    "has_more"
  ],
  "additionalProperties": false
}
```

### DailyUsage

```json
{
  "type": "object",
  "properties": {
    "results": {
      "type": "array",
      "items": {
        "$ref": "#/components/schemas/UsageDay"
      }
    },
    "metadata": {
      "$ref": "#/components/schemas/UsageMetadata"
    }
  },
  "required": [
    "results",
    "metadata"
  ],
  "additionalProperties": false
}
```

### UsageKeyMetadata

```json
{
  "type": "object",
  "properties": {
    "key_alias": {
      "type": "string",
      "nullable": true
    },
    "team_id": {
      "type": "string",
      "nullable": true
    },
    "user_id": {
      "type": "string",
      "nullable": true
    },
    "user_email": {
      "type": "string",
      "nullable": true
    }
  },
  "required": [
    "key_alias",
    "team_id",
    "user_id",
    "user_email"
  ]
}
```

### UsageKeyBucket

```json
{
  "type": "object",
  "properties": {
    "metrics": {
      "$ref": "#/components/schemas/UsageMetrics"
    },
    "metadata": {
      "$ref": "#/components/schemas/UsageKeyMetadata"
    }
  },
  "required": [
    "metrics",
    "metadata"
  ]
}
```

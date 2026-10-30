# Logs and usage API — generated reference

Generated from OpenAPI; existing authorization and billing remain unchanged. See core-contract-completion.md.

[OpenAPI](../../../contracts/usage/openapi.json)

| Operation | Behavior | HTTP statuses |
| --- | --- | --- |
| `GET /spend/logs/v2` | Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /spend/logs/ui` | Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /spend/logs/session/ui` | Empty session_id returns empty page before authentication under existing behavior | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /spend/logs/ui/{request_id}` | Out-of-scope and missing records are 404; admin cross-user reads audited; missing bodies may be empty and non-JSON upstream bodies strings; not every failure has a usage record | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /user/daily/activity` | Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /user/daily/activity/aggregated` | Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /team/daily/activity` | Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /team/daily/activity/aggregated` | Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /organization/daily/activity` | Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted | 200, 400, 401, 403, 404, 409, 500, 503 |

### GET /spend/logs/v2

Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    },
    "description": "Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day."
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    },
    "description": "Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day."
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

Scoped logs; invalid page defaults to 1, page_size outside 1..200 defaults to 50; UTC inclusive dates; exact timestamps; invalid or reversed windows return 400; session grouping before pagination; undeclared filters not guaranteed

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    },
    "description": "Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day."
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    },
    "description": "Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day."
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

Empty session_id returns empty page before authentication under existing behavior

```json
[
  {
    "in": "query",
    "name": "start_date",
    "schema": {
      "type": "string"
    },
    "description": "Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day."
  },
  {
    "in": "query",
    "name": "end_date",
    "schema": {
      "type": "string"
    },
    "description": "Accepts UTC dates, UTC date-times, or RFC3339. Timestamp end bounds are not extended by one day."
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

Out-of-scope and missing records are 404; admin cross-user reads audited; missing bodies may be empty and non-JSON upstream bodies strings; not every failure has a usage record

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

Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted

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

Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted

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

Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted

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

Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted

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

Scoped daily aggregates; paged totals cover current page, aggregated totals entire range; invalid dates leave bounds open; unknown cache tokens omitted

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


## Schemas

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
  "description": "Management error envelope; code matches HTTP status and failures never use a success object."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
      "description": "Currently null."
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
  "description": "Error fields are combined at the same object level to avoid replacing the nested error."
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
      "description": "Preserve arbitrary JSON values, unknown fields and null"
    },
    "response": {
      "x-go-type": "rawjson.RawMessage",
      "x-go-type-import": {
        "name": "rawjson",
        "path": "encoding/json"
      },
      "description": "Preserve arbitrary JSON values, unknown fields and null"
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
      "description": "Preserve arbitrary JSON values, unknown fields and null"
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
    "description": "Preserve arbitrary JSON values, unknown fields and null"
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

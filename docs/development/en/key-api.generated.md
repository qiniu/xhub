# Key management API — generated reference

Generated from OpenAPI; no production handler is replaced. See key-contract.md for compatibility and verification boundaries.

[OpenAPI](../../../contracts/keys/openapi.json)

| Operation | Behavior | HTTP statuses |
| --- | --- | --- |
| `POST /key/generate` | Create a personal key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /key/list` | List keys within permission scope | 200, 400, 401, 403, 404, 409, 500, 503 |
| `GET /key/info` | Read one key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /v2/key/info` | Read one key by body | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/update` | Update a key while preserving omitted fields | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/block` | Block a key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/unblock` | Unblock a key | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /key/delete` | Delete one or more keys | 200, 400, 401, 403, 404, 409, 500, 503 |

### POST /key/generate

Only creation returns plaintext; defaults to current user, team derived from membership; blank budget still obeys personal and parent caps. Supported contract is one object; legacy handler also accepts empty body or null and does not strictly reject every undeclared input.

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

scope=personal is the logged-in owner only, including admins; invalid pagination or sorting returns 400; empty results have one total page. Legacy status and expand parameters are ignored, not effective filters.

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
    "description": "Legacy name, actually filters by record ID."
  },
  {
    "name": "substring_matching",
    "in": "query",
    "required": false,
    "schema": {
      "type": "boolean"
    }
  },
  {
    "name": "organization_id",
    "in": "query",
    "required": false,
    "allowEmptyValue": true,
    "description": "Exact match on the key owner's current team organization; intersected with authorization and other filters. Empty means no restriction; teamless keys do not match a nonempty value.",
    "schema": {
      "type": "string"
    }
  }
]
```


### GET /key/info

Read one key

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

Supports key, not a keys array; body key overrides query; legacy query fallback is a compatibility boundary.

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

Returns the key object without a data envelope; no plaintext, ownership or historical spend changes.

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

Block a key

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

Unblock a key

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

Authorizes every target before deleting individually; storage failure may leave partial deletion, not a batch transaction; repeated deletion returns 404.

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
  "description": "Omission preserves (creation has no separate cap); null or empty string clears; zero blocks this key; strings must be finite nonnegative numbers."
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
  "description": "Null or empty clears; strings must represent nonnegative int32 integers."
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
      "description": "Empty array or all-model sentinel inherits without widening parent permissions."
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
      "description": "Omission preserves; null or empty clears and inherits."
    },
    "duration": {
      "type": "string",
      "nullable": true,
      "description": "Optional at creation; omission, null or empty preserves expiry on update; nonempty must be a valid positive duration such as 30d."
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
      "type": "string",
      "nullable": true
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
      "description": "Empty array or all-model sentinel inherits without widening parent permissions."
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
      "description": "Omission preserves; null or empty clears and inherits."
    },
    "duration": {
      "type": "string",
      "nullable": true,
      "description": "Optional at creation; omission, null or empty preserves expiry on update; nonempty must be a valid positive duration such as 30d."
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
      "description": "Plaintext on creation; masked on ordinary list and detail; record ID on personal list, not an inference credential."
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
      "description": "Plaintext on creation; masked on ordinary list and detail; record ID on personal list, not an inference credential."
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
      "description": "Plaintext on creation; masked on ordinary list and detail; record ID on personal list, not an inference credential."
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

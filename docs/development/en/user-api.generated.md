# User management API — generated reference

Generated from OpenAPI; no production handler is replaced. See user-contract.md for compatibility and verification boundaries.

[OpenAPI](../../../contracts/users/openapi.json)

| Operation | Behavior | HTTP statuses |
| --- | --- | --- |
| `GET /user/list` | List users within permission scope | 200, 401, 403, 404, 500, 503 |
| `GET /user/info` | Read self or visible user | 200, 400, 401, 403, 404, 500, 503 |
| `GET /v2/user/info` | User detail alias | 200, 400, 401, 403, 404, 500, 503 |
| `GET /user/filter/ui` | Admin picker up to 500 users | 200, 401, 403, 404, 500, 503 |
| `GET /user/available_users` | Admin picker alias | 200, 401, 403, 404, 500, 503 |
| `POST /user/new` | Create user with optional enterprise identity | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/update` | Update user with existing authorization | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/bulk_update` | Atomically update budgets and reset days | 200, 400, 401, 403, 404, 409, 500, 503 |
| `POST /user/delete` | Delete user except self | 200, 400, 401, 403, 404, 409, 500, 503 |

### GET /user/list

List users within permission scope

```json
[
  {
    "name": "page",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Current parsing: invalid/page below 1 defaults to 1; page_size outside 1..500 defaults to 100."
  },
  {
    "name": "page_size",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Current parsing: invalid/page below 1 defaults to 1; page_size outside 1..500 defaults to 100."
  },
  {
    "name": "search",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "user_email",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "user_ids",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "role",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "team",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "sso_user_ids",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "organization_ids",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "sort_by",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "sort_order",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  }
]
```


### GET /user/info

Legacy GET object-body user_id overrides query; prefer query. Members receive authorized fields only.

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  }
]
```


### GET /v2/user/info

Legacy GET object-body user_id overrides query; prefer query. Members receive authorized fields only.

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  }
]
```


### GET /user/filter/ui

Admin picker up to 500 users

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "user_email",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  }
]
```


### GET /user/available_users

Admin picker alias

```json
[
  {
    "name": "user_id",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  },
  {
    "name": "user_email",
    "in": "query",
    "schema": {
      "type": "string"
    },
    "description": "Compatibility query string; authorization and combinations checked by service."
  }
]
```


### POST /user/new

Create user with optional enterprise identity

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

Update user with existing authorization

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

Atomically update budgets and reset days

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

Delete user except self

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


## Schemas

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
      "description": "Currently empty placeholder, not organization permissions."
    },
    "models": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "Currently empty placeholder, not available models."
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
      "description": "Currently zero placeholder, not actual key count."
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
  "description": "Single-user compatibility: finite nonnegative numeric strings; empty string/null clears, omission preserves (uncapped on create), zero denies spend. String semantics are checked by the service.",
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
  "description": "Nonnegative integer up to 2147483647, numeric strings accepted; null/empty string clears.",
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
      "description": "Validated compatibility field, not persisted; does not set team member budget."
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
      "description": "Zero disables; 1–31 resets at Beijing midnight, clamped to month end; omission preserves, null invalid."
    },
    "sso_connection_id": {
      "type": "string"
    },
    "sso_external_id": {
      "type": "string"
    },
    "user_email": {
      "type": "string",
      "description": "Email is trimmed and lowercased by existing rules."
    },
    "password": {
      "type": "string",
      "description": "8–72 UTF-8 bytes, not characters.",
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
      "description": "Assigns organization administration, not ordinary affiliation."
    }
  },
  "required": [
    "user_email",
    "password"
  ],
  "additionalProperties": true,
  "description": "Platform admin creates without a key; default role user, internal_user/proxy_admin aliases accepted; SSO pair must be supplied together; extra fields may be ignored.",
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
      "description": "Validated compatibility field, not persisted; does not set team member budget."
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
      "description": "Zero disables; 1–31 resets at Beijing midnight, clamped to month end; omission preserves, null invalid."
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
  "description": "Single complete JSON object. Target defaults to self; members edit own profile only, management fields do not grant privileges; budget writes separately authorized. Admin password and other fields are not one transaction.",
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
  "description": "Platform admin only; at most 1000 users and 1MiB body; unique IDs, all_users includes disabled/admin users; atomic failures do not change spend.",
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

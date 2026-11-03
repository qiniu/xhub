# Supplier management API — generated reference

Generated from the pilot OpenAPI source. No production route or client is replaced. See supplier-contract-pilot.md for compatibility and verification boundaries.

[OpenAPI](../../../contracts/suppliers/openapi.json)

| Operation | Behavior | HTTP statuses |
| --- | --- | --- |
| `GET /credentials` | List suppliers (two equivalent array aliases). | 200, 400, 401, 403, 500, 503 |
| `POST /credentials` | Save supplier; duplicate creation replaces existing configuration. | 200, 400, 401, 403, 500, 503 |
| `GET /credentials/by_name/{credential_name}` | Read supplier with masked secret strings. | 200, 400, 401, 403, 404, 500, 503 |
| `PATCH /credentials/{credential_name}` | Merge patch; upsert when missing. | 200, 400, 401, 403, 500, 503 |
| `DELETE /credentials/{credential_name}` | Delete supplier; repeated deletion returns deleted=0. | 200, 400, 401, 403, 500, 503 |

### GET /credentials

List suppliers (two equivalent array aliases).


### POST /credentials

Save supplier; duplicate creation replaces existing configuration.

```json
{
  "required": false,
  "description": "Empty body acts as an empty object; nonempty body must be a JSON object.",
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/CredentialInput"
      },
      "example": {
        "credential_name": "example-provider",
        "credential_info": {
          "custom_llm_provider": "openai"
        },
        "credential_values": {
          "api_base": "https://provider.example/v1",
          "api_key": "example-not-a-secret"
        }
      }
    }
  }
}
```


### GET /credentials/by_name/{credential_name}

Read supplier with masked secret strings.

```json
[
  {
    "$ref": "#/components/parameters/CredentialName"
  }
]
```


### PATCH /credentials/{credential_name}

Merge patch; upsert when missing.

```json
[
  {
    "$ref": "#/components/parameters/CredentialName"
  }
]
```

```json
{
  "required": false,
  "description": "Empty body acts as an empty object; nonempty body must be a JSON object.",
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/CredentialInput"
      },
      "example": {
        "credential_name": "example-provider",
        "credential_info": {
          "custom_llm_provider": "openai"
        },
        "credential_values": {
          "api_base": "https://provider.example/v1",
          "api_key": "example-not-a-secret"
        }
      }
    }
  }
}
```


### DELETE /credentials/{credential_name}

Delete supplier; repeated deletion returns deleted=0.

```json
[
  {
    "$ref": "#/components/parameters/CredentialName"
  }
]
```

```json
{
  "required": false,
  "description": "Empty body acts as an empty object; nonempty body must be a JSON object.",
  "content": {
    "application/json": {
      "schema": {
        "$ref": "#/components/schemas/CredentialInput"
      },
      "example": {
        "credential_name": "example-provider",
        "credential_info": {
          "custom_llm_provider": "openai"
        },
        "credential_values": {
          "api_base": "https://provider.example/v1",
          "api_key": "example-not-a-secret"
        }
      }
    }
  }
}
```


## Schemas

### CredentialInput

```json
{
  "type": "object",
  "description": "Create/update object; no required business fields, extensions retained, top-level password ignored and jsonrpc rejected.",
  "properties": {
    "credential_name": {
      "nullable": true,
      "description": "Name; generated when absent or non-string on create."
    },
    "credential_info": {
      "nullable": true,
      "description": "Provider metadata; legacy API accepts any JSON."
    },
    "credential_values": {
      "nullable": true,
      "description": "Credential object is masked on read and omitted on create. PATCH ignores blank/masked secret fields and non-object values."
    },
    "created_at": {
      "nullable": true,
      "description": "Creation time; legacy caller overrides are accepted."
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
      "description": "Provider metadata; legacy API accepts any JSON."
    },
    "credential_values": {
      "nullable": true,
      "description": "Credential object is masked on read and omitted on create. PATCH ignores blank/masked secret fields and non-object values."
    },
    "created_at": {
      "nullable": true,
      "description": "Creation time; legacy caller overrides are accepted."
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
  "description": "Supplier read/save result; raw JSON Go representation preserves extensions and nulls."
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
  "description": "Supplier list response; data and credentials are equivalent compatibility aliases."
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
  "description": "Deletion result; deleted counts actual removals and may be zero on repeats."
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

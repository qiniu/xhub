# provider

[简体中文](readme_cn.md)

This package defines upstream execution protocols. Add providers and deployments for existing protocols in the UI. Implement new protocols in `internal/provider/<supplier>/` and import them in `all/all.go`. See [Add model providers and implement new protocols](../../docs/development/en/provider-catalog.md) for NewAPI page operations, provider registration, execution wiring and acceptance checks.

## Integration from other modules

Integration here means other Go packages in this repository. Go internal import rules restrict imports from outside the repository; HTTP clients use mounted gateway endpoints.

This directory navigates the protocol system without a root constructor. Import all at startup, match Hits through registry, validate deployments, and execute through dataplane. New protocols require endpoint, transport, and measurement registration.

| Entry point or resource | Usage and result |
| --- | --- |
| all | Register all suppliers at startup |
| registry.Match / deployment.ResolveHit | Match entry points and validate bindings |
| measurement / contract | Fact extraction and type contracts |

### Integration demo

Run the following commands from the repository root.

```sh
go test ./internal/provider/... -count=1
```

Runs registration, matching, capability, and binding tests. See [all](all/readme.md) for the minimal blank-import demo and [deployment](deployment/readme.md) for binding.

### Errors, state, and side effects

Registration creates neither accounts nor connections. Configure transport/endpoint_types explicitly and retain task deployment/creation facts across polling.

## Choose the integration level

- **NewAPI / company relay / OpenAI-compatible service**: create a compatible named credential with the actual address and key, then explicitly select a registered transport such as `bypass_openai_chat`. No new provider package or per-model registration is required for ordinary compatible models.
- **Existing protocol, different authentication or paths**: add a supplier package and register a `Transport`, optionally a `Supplier` and `Model`, then import it in `all/all.go`. See `qiniu/seedance.go`. Pricing entries are not protocol implementations.
- **Suno music tasks / entirely new payload protocol**: no built-in Suno executor exists. Add a public endpoint, entry and task handling, measurement and UI support as needed. A directory, logo, credential field or transport registration alone does not complete integration.

## Read the implementation in this order

1. `contract/type.go`: actual `Transport`, `Action`, `AuthConfig` and `TaskBilling` fields.
2. `capability/endpoints.go`: endpoint catalog and compatibility; new media protocols require explicit entries.
3. `registry/registry.go`, `registry/models.go`: transport and model registration, not account creation.
4. `deployment/validate.go`, `deployment/catalog_binding.go`: transport, provider restrictions, catalogs and fixed model validation.
5. `../dataplane/transport/bypass_url.go`, `../dataplane/execution/official.go`: URL composition, native execution, task pinning and settlement.
6. `all/all.go`: compile-time loading; rebuild and restart the gateway. Uploaded Go plugins are not hot-loaded.

Credential type, connection name, `catalog_id`, upstream model ID, public model name and `transport` are different fields. Setting `custom_llm_provider: suno` does not select a music protocol. `model_info.transport` must be registered. Standard client endpoints and matching Chat Completions, Responses and Messages Bypass endpoints derive from execution capability. Other Bypass endpoints require explicit `endpoint_types`.

| Directory | Responsibility |
|---|---|
| [contract](contract/readme.md) | Shared value contracts |
| [capability](capability/readme.md) | Public endpoints and protocol matching |
| [registry](registry/readme.md) | Built-in registration and path matching |
| [deployment](deployment/readme.md) | Explicit bindings, validation and candidates |
| [measurement](measurement/readme.md) | Response/SSE usage and task billing facts |
| [openai](openai/readme.md) | OpenAI native registration |
| [anthropic](anthropic/readme.md) | Anthropic Messages registration |
| [google](google/readme.md) | Gemini and Vertex registration |
| [qiniu](qiniu/readme.md) | Qiniu task and FAL registration |
| [volcengine](volcengine/readme.md) | Ark task registration |
| [all](all/readme.md) | Startup composition of all vendors |

Vendors depend on registry/measurement and contract. Deployment depends on registry, capability and llm/dialogue. Dataplane executes requests; gateway owns authentication, persistence and composition.

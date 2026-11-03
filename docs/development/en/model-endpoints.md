# Models and endpoints

A deployment selects a saved provider, upstream model, public model name, transport and price. These are separate contracts.

## Example

![English model deployment form](../../assets/provider-guide/deployment-form-en.jpg)

## Contract

| Field | Role |
| --- | --- |
| model_name | Client model and routing/authorization name |
| litellm_params.model | Actual upstream model ID |
| litellm_credential_name | Saved provider connection |
| model_info.transport | Required registered executor |
| endpoint_types | Standard and matching Chat/Responses/Messages Bypass capabilities derive from transport; other Bypass/Fal explicit |

Unknown/missing transports are rejected. Prices do not define capabilities. /public/endpoints is the registry; /model/available returns the current identity's actual executable endpoints.

Model discovery tries /models and /v1/models only after 404, within one 20-second limit. Other errors remain errors; an empty successful list is not retried. Discovery does not change the inference URL.

Valid deployments automatically expose the matching Chat Completions, Responses or Messages Bypass endpoint, including existing deployments without rewriting `endpoint_types`. Clients choose standard processing or passthrough by request path. A mismatched upstream protocol is rejected, not converted. Other Bypass endpoints still require explicit declarations. Model permissions, budgets and disabled status still apply. Passthrough retains its existing native behavior and does not promise all conversion, guardrail or cache features of standard endpoints.

## Source and verification

[internal/provider/deployment/validate.go](../../../internal/provider/deployment/validate.go)

~~~bash
go test ./internal/provider/... -count=1
~~~

[Test prerequisites](testing.md) · [Next: provider-catalog](provider-catalog.md)

# Developer guide

| Task | Start here |
| --- | --- |
| Add a provider / protocol | [Complete Go registration example](provider-catalog.md) |
| Change model forms / endpoints | [Models](model-endpoints.md), [Credential forms](provider-credential-forms.md) |
| Change routing | [Template JSON](routing.md), [Groups](routing-groups.md) |
| Change permissions / limits | [Authorization](permissions.md), [Quota chain](quota-chain.md) |
| Change billing / guardrails | [Pricing](pricing.md), [XGo example](guardrails.md) |
| Find HTTP handlers | [API and source](api-reference.md) |
| Verify changes | [Commands](testing.md), [Regression](regression.md), [Browser E2E](e2e-regression.md) |
| Improve integrity while preserving features | [API contracts and incremental decoupling plan](interface-integrity-roadmap.md), [Interface and test baseline](interface-integrity-baseline.md) |

## Make a change

~~~bash
 git status --short
 rg 'RegisterTransport' internal/provider
 go test ./internal/provider/... -count=1
~~~

Read the applicable AGENTS.md, CLAUDE.md and package README. Product changes need unit, backend regression and browser E2E tests. Use isolated schemas and clean up test records.

## Preserve these rules

- Reauthorize on the server; hiding buttons is not authorization.
- Templates cannot expand model access. Never switch providers after emitting stream content.
- Read historical price snapshots. Commit PostgreSQL before acknowledging the Redis queue.
- Maintain both UI languages; keep request examples and prompts in English.
- Do not edit schema.d.ts manually. Go OpenAPI generation is not available; gen:api fails explicitly. Verify contracts through real HTTP regression and frontend consumer tests.

[Request lifecycle](architecture.md) · [Source map](implementation.md) · [User guide](../../user-guide.md)

- [Prompt and search test contracts](real-probe-contracts.md)

# Test commands and prerequisites

## Choose the verification layer

| Change | Layer |
| --- | --- |
| Parsers / rates / conversion | Package unit tests |
| HTTP / authorization / persistence | Backend regression |
| UI through data plane | Real-browser E2E |
| Docs | Languages, links, highlighting and image loading |

~~~bash
go test ./internal/provider/... ./internal/router ./internal/catalog -count=1
XHUB_REGRESSION_STRICT=1 go test ./cmd/regression -count=1
make e2e-offline
~~~

PostgreSQL defaults to 127.0.0.1:5433; override with XHUB_TEST_DATABASE_URL. Database URL/connection/create/cleanup errors fail even without XHUB_REGRESSION_STRICT. Start PostgreSQL before running database tests. Regression creates and cleans an isolated schema per case. Redis tests need a dedicated instance.

## Frontend / website

~~~bash
cd frontend
npm run test:unit -- src/components/route_templates/templateForm.test.ts
npm run test:component -- src/components/route_templates/RouteTemplateSelect.test.tsx
npm run test:types
~~~

~~~bash
cd website
npm test
npm run build
npm run audit:links
npm run test:e2e
~~~

## Side effects

| Command | Boundary |
| --- | --- |
| make regression | Local upstream; free, database required |
| make e2e-offline / e2e-model-endpoints | Local upstream; free |
| make regression-live / e2e / e2e-all | Real providers; incurs charges |
| make testdata | Rebuilds shared xhub/public and Redis DB 1; no inference |

[Backend](regression.md) · [Browser](e2e-regression.md) · [Paid verification](regression-live.md)

Reports must record commands, pass/fail/skip, cleanup and external boundaries. Go OpenAPI sources are not available; routes.json cannot stand in for response schemas.

### Test gate evidence

`scripts/regression.sh` consumes `go test -json` and requires at least one passed leaf test at the requested `-run` depth. An unmatched child, empty run, skipped-only selection, malformed event stream or incomplete package cannot pass. Optional external-provider skips remain separate from passed counts. Direct `go test -run` retains Go’s default empty-match behavior; use the script for acceptance.

Each run writes `.e2e/runs/regression-*/regression.log`, `events.jsonl`, and `summary.json`. The original Go failure code takes precedence over later reporting errors. Browser runs also reject empty/skipped-only selections and write `$E2E_RUN_DIR/cleanup-status.json`; nonzero test or cleanup status fails the entrypoint. Acceptance wrappers generate the final report after Redis/lock cleanup, and never reuse `.e2e/current` as evidence for a new run.

Gate self-tests (isolated subprocesses, real Go matching, and a local PostgreSQL schema):

```bash
go test -race ./cmd/regression/testsupport -count=1
python3 -m unittest scripts.test_regression_results scripts.test_test_gates scripts.test_regression_output scripts.test_e2e_summary scripts.test_e2e_model_endpoints -v
node --test frontend/e2e/checklist-reporter.test.cjs
```

Forced termination such as SIGKILL can prevent any cleanup/report hook from running; absence of a complete report is not success. These gate tests do not verify paid providers or production availability.

# Isolated database fixtures

[简体中文](readme_cn.md) · [Feature implementation reference](../../../docs/development/zh-CN/implementation.md)

## Responsibilities and behavior

DatabaseURL reads XHUB_TEST_DATABASE_URL or returns the local Compose default, not an empty string. Postgres creates a private schema, returns a URL with search_path, and registers cleanup.
Invalid URLs, unreachable databases, schema creation and cleanup errors always fail, with or without strict mode. Operations have a five-second timeout and cleanup targets only the current schema. IAM and Store must be opened against the returned schema URL. Forced process termination can leave schemas requiring targeted cleanup.
This package imports neither IAM nor gateway, avoiding cycles. Never expose DSNs or credentials in reports.

## Source responsibilities and entry points

### postgres.go

- [`func DatabaseURL() string`](postgres.go) — DatabaseURL returns the DSN the suites should connect to.
- [`func Postgres(t *testing.T, prefix string) string`](postgres.go) — Creates and cleans an isolated schema, returning its search_path URL. Errors identify the stage and type without leaking the DSN.

## External HTTP boundary

This directory registers no direct HTTP route. Higher layers call its Go API; trace catalog dispatch or host calls through the dependency chain.

## Verification and maintenance

| Test file | Scenario entry points |
| --- | --- |
| postgres_test.go | TestDatabaseURL, TestPostgresFailureModes, TestPostgresLifecycle |

```bash
go test ./cmd/regression/testsupport -count=1
```

Start PostgreSQL before testing; connection and cleanup failures are always fatal to acceptance. Redis and live providers require separate configuration. Update this reference and feature documentation after contract changes.

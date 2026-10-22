# Trace a failed request

Keep the timestamp, public model, HTTP status and x-litellm-call-id. Find the matching request log before changing configuration.

## Example

~~~bash
curl -i "$XHUB_BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $XHUB_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"company-chat","messages":[{"role":"user","content":"Reply with ready."}]}'
~~~

## Contract

This diagnostic makes a model call and may incur provider charges.

| Symptom | Check |
| --- | --- |
| 401 | XHub key validity; do not send the upstream key |
| 403 / concealed 404 | Current membership, ownership and model scope; 403 budget_exceeded means exhausted budget |
| 429 | RPM/TPM and provider response |
| No deployment | Enabled status, transport and endpoint capability |
| Stream interrupted | Events and final log; HTTP status may already be sent |
| Missing cost | Actual usage, rate snapshot, PostgreSQL and Redis backlog |

Remove credentials, personal information and signed media URLs before sharing evidence. Do not delete production data to repair a fixture.

## Source and verification

[internal/gateway/spend.go](../../../internal/gateway/spend.go)

~~~bash
go test ./internal/gateway/usage -count=1
~~~

[Test prerequisites](testing.md) · [Next: runtime](runtime.md)

### Independent disk usage spool

Compose mounts `xhub-usage-spool:/var/lib/xhub/usage-spool` and enables
`XHUB_USAGE_SPOOL_DIR`. On final usage, the gateway saves the complete `UsageRecord`
(including reservation ID, frozen prices, and bodies permitted by existing logging
settings), syncs the file, atomically renames it, and syncs the directory before
synchronously committing PostgreSQL. It removes the snapshot only after database
confirmation. A complete PG outage without Redis can therefore recover after a
restart. Recovery settles usage; it never repeats the upstream call. Existing PG
`usage_pending` and old Redis queues still drain; new spool-enabled calls do not
also enqueue Redis hot charges.

This adds disk sync and database commit latency per call. Throughput depends on
the disk and database. Use a persistent volume, not the container writable layer or
a temporary directory. Each replica requires its own directory and volume; an
exclusive process lock rejects shared use. Preserve the entire directory during
migration or shutdown, not just the database. Remove an old volume only after its
queue has drained.

Startup capacity settings accept positive integers:

| Variable | Default | Meaning |
| --- | --- | --- |
| `XHUB_USAGE_SPOOL_MAX_BYTES` | `1073741824` | Pending/quarantine bytes plus inflight reservations |
| `XHUB_USAGE_SPOOL_RECORD_BYTES` | `16777216` | Complete record limit and conservative inflight reservation |
| `XHUB_USAGE_SPOOL_MAX_ENTRIES` | `8192` | Pending records plus inflight calls |

Default byte limits allow at most 64 simultaneous inflight reservations, fewer
with pending records. Admission tests actual writing and sync before upstream
submission. Directory/file modes are `0700`/`0600`. Full, unwritable or quarantined
storage returns `503 billing_storage_unavailable` with
`X-XHub-Usage-Persistence: unavailable`. Pending disk charges reject new hosted
search soft-budget calls with `503 billing_storage_pending` rather than relying on
stale balances. Existing hard-budget holds remain. Until recovery, usage pages
contain settled charges only, not all incurred charges.

Records include a version and SHA-256 checksum. Corrupt, unsupported-version or
conflicting snapshots are preserved in `quarantine/`, never silently deleted.
Keep evidence and reconcile or restore it; clearing the queue loses billing data.
Database call IDs, complete-snapshot SHA-256 fingerprints and reservation
reconciliation prevent duplicate accounting. A different pending payload or
committed fingerprint with the same ID blocks acknowledgement and retains disk
evidence. Startup idempotently adds `usage_fingerprint`; historical events retain
an empty fingerprint and strict replay refuses to acknowledge them automatically.
Reconcile those records explicitly rather than inventing historical evidence.
Conflicts retain their payload and produce errors; retries wait at least 30 seconds
per failed record without blocking other valid records. Logs contain request IDs,
hashed filenames and errors, not full payloads. Do not attach usage files or user
bodies to issues. Configuration changes require a restart.

Limits: non-Compose deployments without a configured directory retain the old
persistence path. Logical reservation cannot prevent other processes filling the
disk after admission, hardware failure, or process exit before final usage is
persisted. Oversized records are not truncated; a spool failure still attempts PG
and Redis. If all media fail, only errors and existing hard-budget holds remain;
actual usage has not been durably saved. Mutable official asynchronous task
lifecycles still use `RecordTaskUsage` and are outside this final-usage spool.

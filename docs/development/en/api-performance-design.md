# API admission, usage flushing, and log queries

This document describes current execution paths and observable limits.

## Request admission

[Gateway checks](../../../internal/gateway/limits.go) read current key, user, team, and organization state and enforce model access, budgets, and rates. Initial admission uses QuotaAndRates; target rechecks use QuotaPath without consuming minute capacity again. Project budgets are parallel ceilings.

[Quota calculation](../../../internal/iam/quota.go) builds the relevant organization tree and handles fixed allocations, shared budgets, and descendant reservations. Checks combine persisted PostgreSQL spending with Redis hot spending. Request cost is not guaranteed to depend only on path depth. Checking is not atomic reservation before forwarding and cannot guarantee that concurrent requests never overspend. Personal callers without a team still have user and key ceilings checked; unset ceilings do not limit that level, while explicit zero ceilings apply.

Storage failure responses depend on the branch: unavailable authorization storage returns 503; identity reads and quota-chain errors can enter a 401 branch. Exceeded budgets return 403 (`budget_exceeded`); RPM/TPM limits return 429.

## Usage persistence

The [Redis usage queue](../../../internal/live/redis.go) deduplicates by request_id, enqueues events, and increments subject hot spending. Events retain call-time identity, price, and usage snapshots. Deduplication identifiers have no TTL. Publishing does not wait for disk fsync or replica quorum; reliability depends on Redis deployment persistence.

[Background flushing](../../../internal/dataplane/runtime/live.go) runs on a periodic timer and reads at most 500 events per iteration. general_settings.spend_log_flush_interval_seconds defaults to 5 seconds when omitted, accepts integers from 1 through 86400, rejects explicitly invalid configuration at load time, and takes effect after restart. Invalid directly constructed settings fall back to 5 seconds. See [configuration](../../../internal/config/config.go).

Flushing first calls [RecordUsage](../../../internal/iam/usage.go), persisting events, bodies, daily aggregates, and hierarchical spending in a PostgreSQL transaction with request-level idempotency. Only after commit does flushing compare the Redis queue head, acknowledge the batch, and subtract hot spending. Database failure retains the batch. Corrupt events allow only the preceding valid prefix to flush; a corrupt head remains until repaired.

Database commit and Redis acknowledgement are separate operations. Failed acknowledgement can cause retries; database idempotency prevents duplicate accounting. Budget observations can temporarily include committed spending and unacknowledged hot increments. Continuous backlog draining and consumer leases across instances are not guaranteed. The default 5 seconds is a trigger interval, not a log visibility guarantee; backlogs, failures, and transaction duration can delay visibility. Cancellation lets an active flush finish without an extra queue drain.

## Log queries

[Usage queries](../../../internal/iam/usage_read.go) apply authorization and filters first. Ordinary logs use limit / offset pagination; session lists group filtered events, count groups, and paginate without a separate session summary table. Sort fields use a whitelist; unknown fields fall back to time ordering. An arbitrary request identifier does not grant access to Redis bodies. Events awaiting persistence can temporarily be absent; the interface has no universal pending state contract.

## Verification boundaries

The [benchmark guide](../../../benchmarks/README.md) describes how to run isolated measurements and interpret the results. Reports such as `benchmarks/reports/staircase/report.md` and `benchmarks/reports/validation/summary.md` are local artifacts ignored by Git; they are not included in a fresh checkout or the published website. Each report describes only its recorded machine, version, dataset, and dependencies and does not guarantee current production throughput or latency. Supplier time, media task duration, and gateway overhead require separate measurement. Implementation tests reside in internal/config, internal/live, internal/dataplane/runtime, internal/iam, and cmd/regression. Real HTTP, PostgreSQL, Redis, and browser tests require isolated data and cleanup. External supplier verification requires separate credentials and explicit opt-in.

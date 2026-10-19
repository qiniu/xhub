# Configuration and storage

Trace settings by owner. Model providers and deployments are managed in the console; configuration internals below are for maintainers.

## Example

~~~bash
rg 'OverlayDB|MergePatch' internal/config internal/store
rg 'SelectRouteTemplate|Compile' internal/iam internal/router internal/gateway
~~~

## Contract

| Data | Owner | Application |
| --- | --- | --- |
| Startup database / listener | internal/config | Process initialization |
| Providers / deployments | Store | Model catalog and execution |
| Templates / bindings | IAM | Whole-template resolution per request |
| Identity / members / keys | IAM | Fresh authorization |
| RPM / TPM / cooldown / usage queue | Redis | Shared hot state where configured |
| Response cache | Process | Lost on restart |
| Usage / snapshots | PostgreSQL | Historical identity and price |

OverlayDB replaces top-level values, including null/0/false/arrays. MergePatch recursively merges maps and replaces arrays. iam.SelectRouteTemplate selects one whole document, not a field merge. Failed identity/template reads must not silently grant access.

## Source and verification

[internal/config/config.go](../../../internal/config/config.go)

~~~bash
go test ./internal/router ./internal/store -count=1
~~~

[Test prerequisites](testing.md) · [Next: routing](routing.md)

## Organization budget periods and usage loading

Organizations support no reset or fixed UTC intervals of 24 hours, 7 days or 30 days. A 30-day interval is not a calendar month. Platform administrators configure budgets, reset periods and RPM/TPM limits; organization administrators maintain names, metadata and authorized routing bindings. Name searches use case-insensitive substring matching; organization IDs match exactly, and combined filters never widen visibility.

Enabling or changing a period retains current spend and schedules the next reset. Saving the same period does not postpone that reset. Cancelling retains spend. A due reset clears only the organization's `spend` budget counter, with an audit in the same transaction. Historical usage events, daily totals and team/user/key counters remain intact. Downtime advances to the current period without accumulating credits. The background loop and reads/admission can catch up; late previous-period bills remain in history without consuming the current organization budget. Existing databases gain the fields idempotently, with reset disabled by default.

Organization relationships grant administration only: add or revoke access. They have no independently editable role or budget, so the invalid editing action is removed. Organization lists explicitly count administrators, not all users in subordinate teams.

Usage views distinguish successful empty results from failures. Aggregation can fall back to real pagination. Total failure shows a persistent error and retry action instead of unknown values as zero. A later-page failure retains completed pages with an incomplete-data notice and prevents a complete export. Date, account and retry generations immediately hide stale values. A separate gateway-count failure explains that visible counts cover recorded model usage only. Organization/team and other entity views follow the same rules; revoked capabilities show access denied.

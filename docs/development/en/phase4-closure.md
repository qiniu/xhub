# Stage 4 closure inventory

Baseline: `939e0d6c0ba174bd03d6dad4c1035da8bf0be8cd`, reviewed 2026-10-28. Stage 4 is **not complete**. No merge or production deployment.

The finite inventory is `internal/gateway/testdata/phase4-closure.json`: all 303 catalog-owned management declarations (296 family, 5 provider files, 2 WebSocket wrappers). The broader ownership snapshot retains 755 catalog declarations and 95 module-only registrations. These are declarations, not counts of supported unique APIs.

Main status: 22 narrowly verified configuration CRUD entries; 94 source-confirmed semantic mismatches/missing executors (some already have unmerged fixes); 8 explicitly unsupported; 179 need semantic acceptance evidence. The latter are **not 179 confirmed defects**. A migrated service prefix does not prove auxiliary operations under that prefix work.

The isolated HTTP census made 909 requests across anonymous, user, and admin identities. Admin responses: 204 HTTP-200, 87 HTTP-404, 7 HTTP-501, 3 HTTP-204, one HTTP-400 and one HTTP-426. 94 entries changed KV state in this fixture. Sequential minimal-body probes share an isolated schema and may reuse IDs. These observations are not functional acceptance, defect counts, permission proof, or valid-input success rates. The provider placeholder represents a non-OpenAI branch. WEBSOCKET-method probes are not real WebSocket handshake/session tests.

## Fixed work batches

| Batch | Scope | Acceptance |
| --- | --- | --- |
| C1 | Service auxiliary operations | Policy tests/resolve/attachments/versions/status, credential migration/model lookup, prompt versions: no operation names stored as resource IDs; retain genuine CRUD and guard execution. |
| C2 | OAuth, onboarding, invitations, JWT aliases | Prove token/identity effects or explicitly reject unsupported operations; preserve SSO and authorization. |
| C3 | Settings, branding, export, scheduler, warming, events, Vantage | Verify read-after-write and actual consumers, or declare unsupported execution. |
| C4 | Model/fallback/public/block aliases | Compare with real model services and invocation; do not change routing/authorization rules. |
| C5 | Analytics, spend, logs | Verify using nonempty successful/failed/billed fixtures; no fake resets or changed budget algorithms. |
| C6 | Provider files and WebSocket wrappers | Verify real transfer/handshake/failure; cover provider dispatch and preserve native data-plane routes. |
| C7 | Root, registry, routes, auxiliary directories | Compare returned data to actual registrations and consumers. |
| integration | Pending changes together | Resolve shared-file conflicts on an isolated integration branch; run real regression, browser, compatibility, permission/budget negatives and unchanged 70% gate. |

First reproduce each semantic defect, then delegate to existing services or explicitly reject unavailable operations. New routes must fail the inventory gate until reviewed. The complete per-route table and conclusions are in the JSON and [Chinese audit](../zh-CN/phase4-closure.md); group source references are evidence, not supported-API documentation.

## Pending and excluded boundaries

PRs #182, #184, #188, #190, #192, #194, #196, #198, #200, #204, #206, #208, #210, #212, #214, #216, #218, #220, #222 remain unmerged; this audit is #186. References indicate related work, not proof every route in a group is fixed. Include module-owned tag/customer lists, readiness, cache management, SkillHub and dead Key controls in integration acceptance. #202 SAML refresh is separate.

138 module-owned catalog entries and 95 module-only registrations retain their existing contracts; this audit does not claim renewed semantic verification of them. 89 removed declarations must stay unmounted. 34 mixed, 11 public and 180 data declarations retain existing behavior and tests. Protocol lifecycle restructuring belongs to stage 5, but known fake-success wrappers cannot be silently excluded.

## Reproduction

The closure gate detects missing/duplicate routes, wrong ownership, invalid statuses, missing observations, unproved verified labels and boundary drift. It validates inventory integrity, not business correctness.

The census uses the explicit `phase4audit` build tag and is excluded from default CI/coverage; it is an observation experiment, not a functional success test.

```bash
GOFLAGS=-tags=phase4audit XHUB_PHASE4_AUDIT_OUTPUT="$PWD/.verification/phase4-closure/http-observations.json" bash scripts/regression.sh '^TestPhase4AuditObservation$'
go test -race ./internal/gateway -run '^Test(Phase4Closure|RouteOwnershipAudit|RouteAudit)' -count=1
```

Current execution evidence is recorded in the PR. Earlier 37 backend and 8 browser results in `compatibility-layer-audit.md` remain historical unless rerun. No production, real SSO or paid-model tests; isolated schema cleanup failure must fail the run.

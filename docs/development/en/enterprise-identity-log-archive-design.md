# Enterprise identity and Kodo archive

Directory synchronization, OIDC login and the Kodo storage adapter have local implementations. Real tenant/account acceptance remains separate. [简体中文](../zh-CN/enterprise-identity-log-archive-design.md).

## Connect in this order

1. Deploy a public HTTPS origin and a stable server master key of at least 32 characters.
2. SSO and SCIM configurations are managed independently; creating or deleting one does not create or delete the other. For provisioning, create an organization-bound directory in the administrator panel. Save the one-time SCIM credential; register the generated connection URL in the identity platform.
3. Provision test users and groups first. Map groups to teams in the same organization; do not infer priority from incoming group order.
4. Register an OIDC Code + PKCE application and the exact generated callback. Align the stable identity claim with SCIM externalId. Save disabled, then enable and test login.
5. Configure a private Kodo bucket, region, dedicated credentials and prefix. Save disabled; test upload and cleanup before enabling capture.

~~~text
SCIM user/group -> stable externalId -> OIDC identity -> effective team
committed billing -> versioned outbox -> JSONL.gz object + manifest
~~~

## Current boundaries

| Area | Boundary |
| --- | --- |
| Login | No unsynchronized account creation or email-based identity merge |
| Team | Manual override first, then valid current team, then ordered mapping |
| SCIM | Limited filters and attributes; no Bulk or nested groups |
| Archive | One billing event per object, metadata only, PostgreSQL remains authoritative |

These pages do not change the enterprise implementation or claim real Entra/Okta/Qiniu verification. [Validation evidence](enterprise-directory-validation.md).

## Automatic refresh for manually imported SAML metadata

Saved SAML connections expose an independent metadata refresh section. It is off by default; the source must use HTTPS and the interval is 1–24 hours (default 6). Save the settings before selecting **Refresh metadata now**. Keep the existing IdP metadata URL field empty and retain administrator-approved XML. The existing fetch-on-login URL mode remains unchanged. Disabled SSO connections are not refreshed automatically; manual refresh remains available.

The gateway checks due jobs at startup and every minute, independently of the browser. The next attempt uses the earlier of the configured interval and half the remaining metadata lifetime. Failures retry after five minutes, with a minimum one-minute delay. A two-minute database lease prevents concurrent execution across instances; downloads have a 20-second deadline. The UI displays a reloadable snapshot, not external email or messaging alerts.

Only validity metadata may change automatically. Entity identity, certificates, endpoints and other parsed protocol settings must remain unchanged. Certificate rotation requires administrator confirmation. The existing explicit per-connection expired-certificate exception is preserved without disabling signature or assertion-time validation. Failed downloads retain the previous XML; expired XML still blocks sign-in. Successful renewal does not increment the login configuration revision, so an ongoing authentication can finish. Concurrent configuration edits invalidate stale job results.

All endpoints require the existing platform management session and return `Cache-Control: no-store`:

| Method and path | Behavior |
| --- | --- |
| `GET /admin/enterprise/sso/{connection_id}/refresh` | Settings and `last_attempt`, `last_success`, `next_attempt`, `expires_at` (Unix seconds; 0 means absent), plus `error_code` |
| `PUT /admin/enterprise/sso/{connection_id}/refresh` | Save `enabled`, `source_url`, `interval_hours`; this does not claim a successful download |
| `POST /admin/enterprise/sso/{connection_id}/refresh` | Refresh using saved settings: 200 on success, 409 on conflict, 502 on download/validation failure; GET exposes the sanitized cause |

These routes are registered in the migration inventory, not advertised as part of the five core OpenAPI domains. The additive `enterprise_saml_refresh` table stores settings, status and leases and is deleted with its connection. Configuration and execution outcomes are audited. Disable scheduling before rolling back the application; retain the table and last validated XML. No user or budget data rollback is needed.

Local TLS IdP, PostgreSQL and English/Chinese browser tests cover failure recovery, explicit expired-certificate compatibility and signed login completing across a refresh. Enterprise network access, certificate rotation and external provider availability require target-environment verification. Existing connections are never enabled automatically.

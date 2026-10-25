# Standalone API reference

Product guides cover setup and usage; developer docs cover implementation and testing. Request fields and response schemas are rendered from the same OpenAPI sources in a separate Swagger UI. The website API reference link opens `/reference/en/index.html`; Chinese uses `/reference/zh/index.html`. Relative assets support deployment prefixes such as `/xhub/`.

## Scope and boundaries

Only `contracts/{suppliers,users,keys,models,usage}/openapi.json` is loaded: 5 supplier, 11 user, 14 key, 9 model/endpoint and 9 log/usage operations, totaling 48 operations. The build checks every operation against the migration inventory; missing contracts, unregistered operations and unresolved references fail the build. Original JSON is downloadable. Display localization selects descriptive metadata without changing schemas, defaults or examples.

Bypass, streaming inference, SSO/SCIM, organization/team lifecycles and peripheral compatibility APIs are not covered. Existing protocol guides remain available; they are not presented as verified Swagger operations.

## Reading and requests

The initial mode is read-only and never automatically calls production. Enter a gateway you are authorized to access, enable requests, supply gateway credentials in Swagger `Authorize`, then manually use `Try it out` / `Execute`. Cross-origin requests require gateway CORS support. HTTPS pages cannot call HTTP gateways; do not disable browser protections to bypass this.

Targets and authorization stay in memory. Reload returns to read-only; changing domains or targets recreates Swagger and clears authorization. Native Swagger controls remain English; surrounding navigation, status and contract descriptions support Chinese and English without DOM text rewriting. Existing server authorization, budgets and validation still apply.

## Build and compatibility

```bash
npm ci --prefix website
npm test --prefix website
npm run build --prefix website
npm run test:e2e --prefix website
```

Pinned Swagger UI assets ship with the site, without runtime CDN or external validation calls. OpenAPI stays in version control and CI; no production validation middleware is added. Old generated Markdown URLs lead to migration pages. Navigation and search no longer duplicate field tables, and product and developer sidebars are separated.

The real gateway tests are `swagger-reference-en.test.ts` and `swagger-reference-zh-CN.test.ts`. Isolated PostgreSQL verifies browser 401 → authorization → user creation → readback, authorization clearing and cleanup. Release CI builds the site before these tests. Commercial providers, production SSO and production reverse proxies are outside this verification scope.

The shared `scripts/e2e.sh` runner installs locked website dependencies and rebuilds the current reference before browser tests, in both local and CI runs. Both language scenarios are registered in the coverage catalog. Legacy generated-reference operation bookmarks open the corresponding Swagger operation.

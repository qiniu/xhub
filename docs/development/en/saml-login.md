# SAML 2.0 enterprise login

SSO settings support both existing OIDC connections and SAML 2.0. Each connection belongs to one organization. Changing protocols requires a new connection. SAML uses `crewjam/saml` with `goxmldsig v1.6.1` or later to avoid the known XML signature bypass.

## Configuration and registration

1. Create an SSO configuration, choose its organization and SAML 2.0.
2. Enter the public XHub origin, for example `https://console.example.com`. For isolated testing, explicitly enable HTTP testing and use an origin such as `http://111.62.156.153:3000`. This option permits HTTP for XHub addresses and IdP Redirect sign-in endpoints in manually pasted XML. Remote metadata downloads still require HTTPS, and remotely fetched metadata must retain HTTPS sign-in endpoints.
3. Generate the ACS at `/auth/enterprise/saml/acs`, console return URL at `/ui/login`, and SP Entity ID. ACS and return URL must share an origin. Do not register a `/gw/...` callback.
4. Supply an IdP HTTPS metadata URL (Qiniu: `https://sso.qiniu.io/saml2/meta`), or clear the URL and paste a single EntityDescriptor XML document. URL-based metadata is refreshed for each login and must retain the configured issuer. Network failures and expired metadata reject login. Pasted XML requires administrator-managed updates.
5. The default stable identity attribute is `urn:oid:0.9.2342.19200300.100.1.1` (uid). It must occur once with one nonempty value. Transient NameID and email matching are not used for account linking.
6. Save the connection disabled, download SP metadata, and give it to the SSO administrator for registration. Each connection gets a server-generated RSA 2048-bit key and a self-signed certificate valid for one year. The private key is encrypted with the existing enterprise AES-GCM master key; exported metadata contains only the public certificate. Back up the database and preserve the master key.
7. After registration and availability of a current IdP signing certificate, enable the connection and test from a separate browser. Unknown users must be rejected without administrative privilege escalation.

Public metadata: `GET /auth/enterprise/{connection_id}/saml/metadata`, returning XML. The administrator download endpoint is `GET /admin/enterprise/sso/{connection_id}/metadata`, returning `{ "metadata": "..." }` with management-session authorization. Metadata is available before login is enabled.

## Accounts and permissions

Provision ordinary users through the existing SCIM administration workflow first. Their SCIM `externalId` must exactly match the configured uid attribute. SAML does not require SCIM synchronization to remain enabled, but the user, organization and SSO configuration must be active. There is no automatic enrollment, email matching, group-to-admin mapping, direct LDAP password handling, or SAML single logout. Local XHub logout does not end the Qiniu SSO session.

## HTTP testing and callbacks

HTTPS uses Secure, HttpOnly, SameSite=Lax transaction cookies. Explicit HTTP testing uses a different cookie name without Secure. The application session cookie follows the configured origin, never client-supplied forwarding headers.

The cross-site IdP POST verifies signatures, audience, request ID, recipient and validity before staging the stable identity. A 303 redirect then reaches a same-site GET. Only that GET, with the original random browser cookie, may atomically consume the five-minute transaction and create a session. This works without cookies on the cross-site POST. Replays, other browsers, configuration revisions and inactive accounts cannot log in.

HTTP exposes assertions and sessions in transit and is intended for isolated test accounts. Browsers may also warn about submissions from an HTTPS IdP to HTTP. When adopting HTTPS, update the callback, return URL and Entity ID, then register the resulting metadata with the SSO administrator again.

## External conditions checked on 2026-10-20

Qiniu production metadata identifies `https://sso.qiniu.io/saml2/meta` and advertises the Redirect endpoint `https://sso.qiniu.io`. Its advertised signing certificate expired on **2019-05-24**. Its SHA-256 fingerprint is `58:DC:21:82:03:E4:D7:B7:17:03:7E:F6:CF:33:37:4B:DF:B0:50:51:7E:E2:E8:5B:7C:1D:DF:B7:93:A4:DD:1F`.

Disabled configuration and SP metadata export remain available, with an expiry warning; enabling is rejected by default. The isolated testing exception below can allow this pinned expired certificate without bypassing signature validation or changing clocks. For normal use, the SSO administrator must supply a current signing certificate and matching metadata. Successful local IdP tests do not certify real Qiniu login.

The deployment server received HTTP 403 when fetching the same HTTPS metadata, while the desktop could read it. An administrator can paste the trusted XML instead. SAML front-channel login does not require a server-side userinfo call; renewed certificates must also be reimported.

Automatic SP key rotation is not included. Before the one-year certificate expires, arrange a new connection and metadata registration; do not replace the stored certificate without its matching private key.

## Pinned certificate expiry exception for isolated testing

The default remains rejection of expired IdP signing certificates. `allow_expired_idp_certificate` is an independent, default-off connection option that requires SAML, `allow_http=true`, an empty metadata URL and manually pasted XML. The existing expiry warning stays visible. Disable the option after isolated testing; update the IdP certificate before normal rollout.

The per-connection signature verifier copies the trusted certificate objects in memory and only extends an already-expired `NotAfter` for validation. Raw DER, public keys and `NotBefore` remain unchanged. goxmldsig still requires an exact Raw certificate match and validates the complete XML signature. Future certificates, untrusted certificates, tampering, expired assertions and replay remain rejected. No global or assertion clock is changed. This relies on the pinned goxmldsig v1.6.1 certificate matching behavior and must be reverified on dependency upgrades.

export const samlRefresh = {
  "title": "Automatic IdP metadata refresh",
  "hint": "The gateway refreshes saved XML in the background. Identity, certificates and endpoints must remain unchanged. This does not renew expired signing certificates.",
  "enabled": "Automatically refresh metadata",
  "source": "Refresh source (HTTPS)",
  "interval": "Refresh interval (hours, 1–24)",
  "save": "Save refresh settings",
  "now": "Refresh metadata now",
  "load": "Reload refresh status",
  "never": "Not recorded",
  "off": "Automatic refresh disabled",
  "snapshot": "Status is a snapshot; reload to see background results.",
  "last_attempt": "Last attempt",
  "last_success": "Last successful refresh",
  "next_attempt": "Next scheduled attempt",
  "expires_at": "Metadata expires at",
  "saved": "Refresh settings saved",
  "refreshed": "Metadata refreshed successfully",
  "error": "Operation failed. Check the refresh status and try again.",
  "errors": {
    "invalid_baseline": "Saved metadata is invalid; import trusted XML first.",
    "invalid_metadata": "Downloaded metadata is invalid or cannot be used for SAML.",
    "invalid_expiry": "Downloaded metadata has no usable new expiration time.",
    "trust_changed": "Identity, certificates or protocol settings changed. Administrator confirmation is required.",
    "fetch_failed": "Metadata download failed; the existing XML was retained.",
    "invalid_configuration": "Refresh requires saved manual metadata and an HTTPS source.",
    "unknown": "Refresh failed; inspect the gateway logs."
  }
};

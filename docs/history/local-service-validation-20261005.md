# Local macOS validation record

Archived 2026-10-05 from the local-service guide. These earlier observations
have no recorded run date or source commit; they are not a new validation of
the current installer. For current operations see [local service](../local-service.md).

### Validation

Installed both services on macOS and confirmed authenticated context discovery
through the API and viewer proxy. Terminated each service process and verified
that launchd restarted it with a new PID, with the same context persisted in the
database. Verified private credential-file permissions and viewer HTTP response.
Actual reboot was not performed; login startup is configured through installed
LaunchAgent plists with RunAtLoad and KeepAlive.

Automatic-login checks verified initial connection and reload without a credential
form, context discovery, demo/reconnect controls, no Authorization header or
credential storage in the browser, and rejection of cross-origin requests,
unrecognized Host headers, and all write/grant routes. The configuration endpoint
returns only an automatic-login flag, never credential material.


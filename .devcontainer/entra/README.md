# Local Entra ID emulator fixture

Committed directory + credentials for the [entra-emulator](https://github.com/calvinchengx/entra-emulator)
service used by the ENTRA / TRAEFIK VS Code tasks.

| File | Purpose |
|---|---|
| `directory.json` | Admin API export (users, groups, app registrations + secret hashes) |
| `dev.env` | Public client IDs / oauth2-proxy secret / CLI issuer defaults |
| `themeproxy/` | Tiny reverse proxy: public `:8444` → emulator `:18445`, darkens sign-in HTML |

The emulator binary has no theme flag (light Fluent page shell is hardcoded). The
**ENTRA** task therefore runs `themeproxy` on `:8444` and moves the container to
`:18445`, keeping `PUBLIC_ORIGIN=http://localhost:8444` so OIDC discovery URLs
stay stable.

## Dummy users

| UPN | Password |
|---|---|
| `user1@dns-zone-manager.test` | `pass1` |
| `user2@dns-zone-manager.test` | `pass2` |
| `user3@dns-zone-manager.test` | `pass3` |

## Apps

| Display name | Role |
|---|---|
| `dns-zone-manager-api` | Resource API (`appIdUri: api://dns-zone-manager`, scope `access_as_user`) |
| `dns-zone-manager-web` | Confidential client for oauth2-proxy (redirect `http://localhost:8080/oauth2/callback`) |
| `dns-zone-manager-cli` | Public client for `dns-cli auth login` (device code) |

All values in `dev.env` are intentional public development secrets. Do not reuse them outside this stack.

## Regenerating the fixture

```bash
# With the ENTRA task running (themeproxy on :8444), then:
curl -sf -X POST http://127.0.0.1:8444/admin/api/reset -H 'Content-Type: application/json' \
  -d '{"reseed":false}'
# create users / apps / scopes via /admin/api/…, then:
curl -sf http://127.0.0.1:8444/admin/api/export -o .devcontainer/entra/directory.json
# update client IDs / secrets in dev.env to match
```

# oauth2-proxy sign-in templates (dark)

Custom HTML templates for the Traefik OIDC front door
(`http://localhost:8080`). Styled to match the SPA dark theme.

The **TRAEFIK** VS Code task copies these into the
`${COMPOSE_PROJECT_NAME}_oauth2-templates` volume before starting
oauth2-proxy (`OAUTH2_PROXY_CUSTOM_TEMPLATES_DIR=/templates`). Compose
cannot bind-mount workspace paths from inside the nested Docker client
(same constraint as BIND/LGTM config injection).

To refresh after editing:

```bash
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-dns-zone-manager-go_devcontainer}"
sudo docker compose -f .devcontainer/docker-compose.yml \
  --project-name "$COMPOSE_PROJECT_NAME" create oauth2-proxy >/dev/null
VOL="${COMPOSE_PROJECT_NAME}_oauth2-templates"
SEED=$(sudo docker create -v "$VOL:/templates" alpine:3.20)
sudo docker cp .devcontainer/oauth2-proxy/sign_in.html "$SEED:/templates/sign_in.html"
sudo docker cp .devcontainer/oauth2-proxy/error.html "$SEED:/templates/error.html"
sudo docker cp .devcontainer/oauth2-proxy/robots.txt "$SEED:/templates/robots.txt"
sudo docker rm "$SEED" >/dev/null
sudo docker compose -f .devcontainer/docker-compose.yml \
  --project-name "$COMPOSE_PROJECT_NAME" \
  up -d --force-recreate oauth2-proxy
```

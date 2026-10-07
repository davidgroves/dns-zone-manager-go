# Observability config for grafana/otel-lgtm (examples + .devcontainer).
#
# Mounted into the `lgtm` Compose service:
#   otelcol-config.yaml  — scrapes dns-zone-manager /metrics into Prometheus
#   dashboards/          — provisioned Grafana dashboard covering all metrics
#
# otel-lgtm 0.5.0 reads dashboard providers only from
# /otel-lgtm/grafana-v$GRAFANA_VERSION/conf/provisioning/dashboards/.
# provisioning.yaml must be placed there. The JSON files stay under
# /otel-lgtm/grafana/conf/provisioning/dashboards/custom/ (the path inside
# provisioning.yaml). If the provider is missing, Grafana still serves the
# home dashboard from GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH, but the UID
# is not stored and annotation requests return
# "Invalid dashboard UID in annotation request".
#
# Logs: set logging.otlp_endpoint (or OTEL_EXPORTER_OTLP_ENDPOINT) on the API
# so structured logs are exported over OTLP HTTP → collector → Loki.
#
# Grafana UI: http://localhost:3000

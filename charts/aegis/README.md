# aegis

Helm chart for [Aegis](https://github.com/lazzerex/aegis), a TCP/UDP L4 proxy with circuit breaking, load balancing, and health checking. Deploys the Rust data plane (proxies client traffic) and the Go control plane (config management, health checking, Admin API, Prometheus metrics) as a single release.

## Prerequisites

- Kubernetes 1.23+
- Helm 3.8+
- Images for `dataPlane.image.repository` and `controlPlane.image.repository` built from `Dockerfile.data` / `Dockerfile.control` and pushed somewhere your cluster can pull from — no public images are published yet.

## Installing

```bash
helm install aegis charts/aegis \
  --set controlPlane.config.proxy.backends[0].address=db1.internal:5432 \
  --set dataPlane.image.repository=<your-registry>/aegis-data \
  --set dataPlane.image.tag=<tag> \
  --set controlPlane.image.repository=<your-registry>/aegis-control \
  --set controlPlane.image.tag=<tag>
```

Or with a values file — see `values.yaml` for the full list of configurable fields, especially `controlPlane.config.proxy.backends` (required — the chart ships with an empty backend list on purpose).

## Not yet HA-safe

`dataPlane.replicaCount` and `controlPlane.replicaCount` default to `1`. Circuit breaker and rate limiter state live in-memory per instance with no cross-replica coordination — running multiple data-plane replicas today means each pod enforces its own independent limits, not a shared one. Don't scale past 1 until multi-instance coordination lands (see the project's `TASK.md` Phase 3). The bundled `hpa.yaml` template is disabled (`hpa.enabled: false`) for the same reason.

## Auth

Set `controlPlane.apiToken` (or `controlPlane.existingSecret` to reference a Secret you manage) to require a bearer token on the Admin API's mutating routes (`/reload`, `/drain`, backend add/remove). `GET /health`, `/status`, `/backends` are always unauthenticated. Empty token = auth disabled, matching the app's own default.

## TLS on the control→data plane gRPC channel

Set `dataPlane.tls.enabled: true` with either `dataPlane.tls.cert`/`.key` (PEM strings) or `dataPlane.tls.existingSecret`, plus `controlPlane.config.grpc.tlsCaCert` (the CA/cert PEM the control plane should trust). This only covers the internal control-plane → data-plane link, not client-facing proxy traffic.

## Known limitations

- `controlPlane` readiness probe reuses `GET /health`, which always returns 200 regardless of backend or data-plane connection state — it catches a dead process, not a broken link to the data plane.
- No Kubernetes service discovery yet (watching a Service's Endpoints to auto-register backends) — `controlPlane.config.proxy.backends` is a static seed list today. Runtime changes are still possible via the Admin API / `aegis-ctl` after install.

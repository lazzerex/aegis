{{/*
Base name for the release.
*/}}
{{- define "aegis.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name, respecting fullnameOverride / nameOverride.
*/}}
{{- define "aegis.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "aegis.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Per-component fullnames. These are the DNS names other templates wire together —
control-plane's rendered config.yaml depends on aegis.dataPlane.fullname resolving
the same way here as it does wherever the data-plane Service is defined.
*/}}
{{- define "aegis.dataPlane.fullname" -}}
{{- printf "%s-data-plane" (include "aegis.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "aegis.controlPlane.fullname" -}}
{{- printf "%s-control-plane" (include "aegis.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "aegis.dataPlane.grpc.fullname" -}}
{{- printf "%s-grpc" (include "aegis.dataPlane.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "aegis.labels" -}}
helm.sh/chart: {{ include "aegis.chart" . }}
{{ include "aegis.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{- define "aegis.selectorLabels" -}}
app.kubernetes.io/name: {{ include "aegis.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "aegis.dataPlane.labels" -}}
{{ include "aegis.labels" . }}
app.kubernetes.io/component: data-plane
{{- end -}}

{{- define "aegis.dataPlane.selectorLabels" -}}
{{ include "aegis.selectorLabels" . }}
app.kubernetes.io/component: data-plane
{{- end -}}

{{- define "aegis.controlPlane.labels" -}}
{{ include "aegis.labels" . }}
app.kubernetes.io/component: control-plane
{{- end -}}

{{- define "aegis.controlPlane.selectorLabels" -}}
{{ include "aegis.selectorLabels" . }}
app.kubernetes.io/component: control-plane
{{- end -}}

{{/*
Name of the Secret providing AEGIS_API_TOKEN to the control plane, or "" if auth is disabled.
*/}}
{{- define "aegis.controlPlane.tokenSecretName" -}}
{{- if .Values.controlPlane.existingSecret -}}
{{- .Values.controlPlane.existingSecret -}}
{{- else if .Values.controlPlane.apiToken -}}
{{- include "aegis.controlPlane.fullname" . -}}
{{- end -}}
{{- end -}}

{{- define "aegis.controlPlane.tokenSecretKey" -}}
{{- if .Values.controlPlane.existingSecret -}}
{{- .Values.controlPlane.existingSecretKey | default "token" -}}
{{- else -}}
token
{{- end -}}
{{- end -}}

{{/*
Name of the Secret providing the data-plane's TLS cert/key, or "" if TLS is disabled.
*/}}
{{- define "aegis.dataPlane.tlsSecretName" -}}
{{- if not .Values.dataPlane.tls.enabled -}}
{{- else if .Values.dataPlane.tls.existingSecret -}}
{{- .Values.dataPlane.tls.existingSecret -}}
{{- else -}}
{{- include "aegis.dataPlane.fullname" . -}}-tls
{{- end -}}
{{- end -}}

{{- define "baleia.name" }}{{ .Chart.Name | trunc 63 | trimSuffix "-" }}{{ end }}

{{- define "baleia.fullname" -}}
{{- if .Values.fullnameOverride }}{{ .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}{{ .Values.nameOverride | default .Chart.Name | trunc 63 | trimSuffix "-" }}{{ end }}
{{- end }}

{{- define "baleia.labels" -}}
app.kubernetes.io/name: {{ include "baleia.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "baleia.selectorLabels" -}}
app.kubernetes.io/name: {{ include "baleia.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "baleia.image" -}}
{{- if .Values.image.fullImageName }}{{ .Values.image.fullImageName }}
{{- else }}{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}{{ end }}
{{- end }}

{{- define "baleia.trinoService" }}baleia-trino.{{ .Release.Namespace }}.svc.cluster.local{{ end }}
{{- define "baleia.polarisService" }}baleia-polaris.{{ .Release.Namespace }}.svc.cluster.local{{ end }}
{{- define "baleia.temporalService" }}temporal.{{ .Release.Namespace }}.svc.cluster.local{{ end }}

{{- define "baleia.trinoURL" -}}
{{- if .Values.config.TRINO_URL }}{{ .Values.config.TRINO_URL }}
{{- else if and .Values.trino.enabled (include "baleia.trinoAuth" .) }}https://{{ include "baleia.trinoService" . }}:8443
{{- else if .Values.trino.enabled }}http://{{ include "baleia.trinoService" . }}:8080
{{- else }}{{ .Values.config.TRINO_URL }}{{ end }}
{{- end }}

{{- define "baleia.polarisURL" -}}
{{- if .Values.config.POLARIS_URL }}{{ .Values.config.POLARIS_URL }}
{{- else }}http://{{ include "baleia.polarisService" . }}:8181{{ end }}
{{- end }}

{{/* The shared backbone URL (ADR 0022): an explicit one wins, then the
in-chart Redis, else empty (no shared layer). */}}
{{- define "baleia.redisURL" -}}
{{- if .Values.config.BALEIA_REDIS_URL }}{{ .Values.config.BALEIA_REDIS_URL }}
{{- else if .Values.redis.enabled }}redis://{{ include "baleia.fullname" . }}-redis.{{ .Release.Namespace }}.svc.cluster.local:6379/0
{{- else }}{{ .Values.config.BALEIA_REDIS_URL }}{{ end }}
{{- end }}

{{- define "baleia.pluginURL" -}}
{{- if .Values.trino.plugin.url }}{{ .Values.trino.plugin.url }}
{{- else if .Values.trino.plugin.version }}https://github.com/maltzsama/baleia-trino-catalog-store/releases/download/v{{ .Values.trino.plugin.version }}/baleia-catalog-store-{{ .Values.trino.plugin.version }}+trino{{ .Values.trino.image.tag }}.tar.gz
{{- else }}{{ fail "trino.plugin.version is required when trino.enabled is true" }}{{ end }}
{{- end }}

{{- define "baleia.requireSecret" -}}
{{- if not .Values.existingSecret }}{{ if not .Values.secrets.create }}{{ fail "existingSecret or secrets.create=true is required" }}{{ end }}{{ end }}
{{- end -}}

{{- define "baleia.secretName" -}}
{{- if .Values.existingSecret }}{{ .Values.existingSecret }}
{{- else }}{{ include "baleia.fullname" . }}-secret{{ end }}
{{- end }}

{{- define "baleia.secretEnv" -}}
{{- $name := include "baleia.secretName" . -}}
- name: JWT_SECRET
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: JWT_SECRET }
- name: BALEIA_ENCRYPTION_KEY
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_ENCRYPTION_KEY, optional: true }
- name: BALEIA_ENCRYPTION_KEY_PREVIOUS
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_ENCRYPTION_KEY_PREVIOUS, optional: true }
- name: BALEIA_REGISTRY_KEY
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_REGISTRY_KEY, optional: true }
- name: BALEIA_REGISTRY_KEY_PREVIOUS
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_REGISTRY_KEY_PREVIOUS, optional: true }
- name: DATABASE_URL
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: DATABASE_URL }
- name: BALEIA_TRINO_DB_PASSWORD
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_TRINO_DB_PASSWORD }
- name: S3_ACCESS_KEY
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: S3_ACCESS_KEY }
- name: S3_SECRET_KEY
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: S3_SECRET_KEY }
- name: SMTP_PASSWORD
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: SMTP_PASSWORD }
- name: BALEIA_METRICS_TOKEN
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_METRICS_TOKEN, optional: true }
- name: BALEIA_TRINO_EVENTS_TOKEN
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_TRINO_EVENTS_TOKEN, optional: true }
- name: BALEIA_TRINO_PASSWORD
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_TRINO_PASSWORD, optional: true }
{{- end }}

{{- define "baleia.podSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 10001
  runAsGroup: 10001
  fsGroup: 10001
{{- end }}

{{- define "baleia.containerSecurityContext" -}}
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
{{- end -}}
{{/* Non-empty when the in-chart Trino requires authentication (#647). */}}
{{- define "baleia.trinoAuth" -}}
{{- if and .Values.trino.enabled .Values.trino.auth.enabled }}true{{ end }}
{{- end }}

{{/* The Secret holding the in-chart Trino's tls.crt, tls.key and ca.crt. */}}
{{- define "baleia.trinoTLSSecret" -}}
{{- if .Values.trino.auth.tls.existingSecret }}{{ .Values.trino.auth.tls.existingSecret }}
{{- else }}{{ include "baleia.fullname" . }}-trino-tls{{ end }}
{{- end }}

{{/* Where the API and worker find the in-chart Trino's CA bundle. */}}
{{- define "baleia.trinoCAFile" }}/etc/baleia/in-chart-trino-ca/ca.crt{{ end }}

{{/* The CA volume and mount of the API and worker, when Trino is authenticated. */}}
{{- define "baleia.trinoCAMount" -}}
{{- if include "baleia.trinoAuth" . }}
- name: baleia-in-chart-trino-ca
  mountPath: /etc/baleia/in-chart-trino-ca
  readOnly: true
{{- end }}
{{- end }}
{{- define "baleia.trinoCAVolume" -}}
{{- if include "baleia.trinoAuth" . }}
- name: baleia-in-chart-trino-ca
  secret:
    secretName: {{ include "baleia.trinoTLSSecret" . }}
    items: [{ key: ca.crt, path: ca.crt }]
{{- end }}
{{- end }}

{{/* BALEIA_TRINO_CA_FILE for the in-chart Trino's CA, unless config sets a
non-empty one or TRINO_URL points at another Trino. */}}
{{- define "baleia.trinoCAEnv" -}}
{{- $inChart := or (not .Values.config.TRINO_URL) (eq .Values.config.TRINO_URL (printf "https://%s:8443" (include "baleia.trinoService" .))) }}
{{- if and (include "baleia.trinoAuth" .) $inChart (not .Values.config.BALEIA_TRINO_CA_FILE) }}
- name: BALEIA_TRINO_CA_FILE
  value: {{ include "baleia.trinoCAFile" . | quote }}
{{- end }}
{{- end }}

{{/* BALEIA_BI_GATEWAY_ADDR (ADR 0017): empty when the gateway is off. The port
comes from biGateway.port so the listener and its Service cannot disagree. */}}
{{- define "baleia.biGatewayAddr" -}}
{{- if .Values.biGateway.enabled }}:{{ .Values.biGateway.port | int }}{{ end }}
{{- end }}

{{/* BALEIA_BI_GATEWAY_URL: the base URL the gateway rewrites Trino URIs to.
Required when the gateway is on: the BI tool follows those URIs. */}}
{{- define "baleia.biGatewayURL" -}}
{{- if .Values.biGateway.enabled }}{{ required "biGateway.publicURL is required when biGateway.enabled: it must be the URL BI tools reach, e.g. https://bi.example.com" .Values.biGateway.publicURL }}{{ end }}
{{- end }}

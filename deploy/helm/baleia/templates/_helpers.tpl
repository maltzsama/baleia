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
{{- /* Data policies (ADR 0028). Optional so a deployment without them keeps
booting; with them on, the API and worker refuse to start without the token
and the key. config may name the key with an @baleia-secret reference instead. */}}
- name: BALEIA_OPA_TOKEN
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_OPA_TOKEN, optional: true }
{{- if not (hasKey .Values.config "BALEIA_DATA_POLICY_SIGNING_KEY") }}
- name: BALEIA_DATA_POLICY_SIGNING_KEY
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_DATA_POLICY_SIGNING_KEY, optional: true }
{{- end }}
- name: BALEIA_DEFAULT_ENGINE_ID
  valueFrom:
    secretKeyRef: { name: {{ $name }}, key: BALEIA_DEFAULT_ENGINE_ID, optional: true }
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
{{/* Non-empty when the in-chart Trino enforces data policies through an OPA
sidecar (ADR 0028). */}}
{{- define "baleia.dataPolicies" -}}
{{- if and .Values.trino.enabled .Values.trino.dataPolicies.enabled }}true{{ end }}
{{- end }}

{{/* BALEIA_DATA_POLICIES for the API and worker, when the chart renders it and
config does not list it (the deployments already reference every config key). */}}
{{- define "baleia.dataPoliciesEnv" -}}
{{- if and (include "baleia.dataPolicies" .) (not (hasKey .Values.config "BALEIA_DATA_POLICIES")) }}
- name: BALEIA_DATA_POLICIES
  valueFrom:
    configMapKeyRef:
      name: {{ include "baleia.fullname" . }}-config
      key: BALEIA_DATA_POLICIES
{{- end }}
{{- end }}

{{/* Pod annotation that rolls the API and worker when the data policy key or
OPA token given in secrets.values change: both are read from the environment
once, at start, so a running pod would keep signing with the old key or
checking the old token. With existingSecret, restart them yourself. */}}
{{- define "baleia.dataPolicySecretsChecksum" -}}
{{- if and (include "baleia.dataPolicies" .) .Values.secrets.create }}
checksum/data-policy-secrets: {{ printf "%s\n%s" (.Values.secrets.values.BALEIA_DATA_POLICY_SIGNING_KEY | default "") (.Values.secrets.values.BALEIA_OPA_TOKEN | default "") | sha256sum }}
{{- end }}
{{- end }}

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

{{/* BALEIA_BI_GATEWAY_URL: the base URL the gateway rewrites Trino URIs to; the
BI tool follows those URIs. biGateway.publicURL when set, else the gateway
Service's in-cluster address. */}}
{{- define "baleia.biGatewayURL" -}}
{{- if .Values.biGateway.enabled }}{{ .Values.biGateway.publicURL | default (printf "http://%s-bi-gateway.%s.svc.cluster.local:%d" (include "baleia.fullname" .) .Release.Namespace (.Values.biGateway.port | int)) }}{{ end }}
{{- end }}

{{/* Reject a public URL the application cannot serve (ADR 0017): it must be an
http(s) origin with no path and no embedded credentials. The BI data-source
files carry only host and port, so the backend derives them from this origin. */}}
{{- define "baleia.validateBIGatewayURL" -}}
{{- $url := .Values.biGateway.publicURL -}}
{{- if and .Values.biGateway.enabled $url -}}
{{- if not (or (hasPrefix "http://" $url) (hasPrefix "https://" $url)) -}}
{{- fail "biGateway.publicURL must start with http:// or https://" -}}
{{- end -}}
{{- $rest := trimPrefix "https://" (trimPrefix "http://" $url) -}}
{{- if contains "@" $rest -}}
{{- fail "biGateway.publicURL must not carry credentials: BI tools sign in with a BI key, never a URL userinfo" -}}
{{- end -}}
{{- if contains "/" (trimSuffix "/" $rest) -}}
{{- fail "biGateway.publicURL must be an origin with no path (e.g. https://bi.example.com)" -}}
{{- end -}}
{{- end -}}
{{- end }}

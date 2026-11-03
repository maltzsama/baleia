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
{{- define "baleia.trinoCAFile" }}/etc/baleia/trino-ca/ca.crt{{ end }}

{{/* The CA volume and mount of the API and worker, when Trino is authenticated. */}}
{{- define "baleia.trinoCAMount" -}}
{{- if include "baleia.trinoAuth" . }}
- name: trino-ca
  mountPath: /etc/baleia/trino-ca
  readOnly: true
{{- end }}
{{- end }}
{{- define "baleia.trinoCAVolume" -}}
{{- if include "baleia.trinoAuth" . }}
- name: trino-ca
  secret:
    secretName: {{ include "baleia.trinoTLSSecret" . }}
    items: [{ key: ca.crt, path: ca.crt }]
{{- end }}
{{- end }}

{{/* BALEIA_TRINO_CA_FILE for the in-chart Trino's CA, unless config sets it or
points TRINO_URL elsewhere. */}}
{{- define "baleia.trinoCAEnv" -}}
{{- if and (include "baleia.trinoAuth" .) (not .Values.config.TRINO_URL) (not (hasKey .Values.config "BALEIA_TRINO_CA_FILE")) }}
- name: BALEIA_TRINO_CA_FILE
  value: {{ include "baleia.trinoCAFile" . | quote }}
{{- end }}
{{- end }}

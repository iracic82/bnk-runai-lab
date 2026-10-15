{{/*
Object names are fixed (bnk-runai-tenancy, bnk-runai-meter): the binaries hard-code the ConfigMaps they
own (bnk-runai-tenancy-vips, bnk-runai-usage) and the cluster-scoped RBAC is one per cluster, so the chart
is meant to be installed once per cluster. Pod selectors match controller/config/*.yaml exactly so a
release can take over a kubectl-applied install without a selector change.
*/}}

{{- define "bnk-runai-tenancy.name" -}}
bnk-runai-tenancy
{{- end }}

{{- define "bnk-runai-tenancy.meterName" -}}
bnk-runai-meter
{{- end }}

{{- define "bnk-runai-tenancy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Labels shared by every object. */}}
{{- define "bnk-runai-tenancy.commonLabels" -}}
helm.sh/chart: {{ include "bnk-runai-tenancy.chart" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: bnk-runai-tenancy
{{- end }}

{{/* Controller selector (identical to controller/config/deploy.yaml). */}}
{{- define "bnk-runai-tenancy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "bnk-runai-tenancy.name" . }}
{{- end }}

{{- define "bnk-runai-tenancy.labels" -}}
{{ include "bnk-runai-tenancy.selectorLabels" . }}
{{ include "bnk-runai-tenancy.commonLabels" . }}
app.kubernetes.io/component: controller
{{- end }}

{{/* Meter selector (identical to controller/config/meter.yaml). */}}
{{- define "bnk-runai-tenancy.meterSelectorLabels" -}}
app.kubernetes.io/name: {{ include "bnk-runai-tenancy.meterName" . }}
{{- end }}

{{- define "bnk-runai-tenancy.meterLabels" -}}
{{ include "bnk-runai-tenancy.meterSelectorLabels" . }}
{{ include "bnk-runai-tenancy.commonLabels" . }}
app.kubernetes.io/component: meter
{{- end }}

{{/* Secret holding the shared metering token (key "token"). */}}
{{- define "bnk-runai-tenancy.meteringSecretName" -}}
{{- default "bnk-metering-token" .Values.metering.token.existingSecret }}
{{- end }}

{{- define "bnk-runai-tenancy.image" -}}
{{- if .digest }}{{ printf "%s@%s" .repository .digest }}{{ else }}{{ printf "%s:%s" .repository (toString .tag) }}{{ end }}
{{- end }}

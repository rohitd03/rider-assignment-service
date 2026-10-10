{{/*
Chart name, truncated to 63 characters (Kubernetes label limit).
*/}}
{{- define "rider-assignment.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "rider-assignment.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "rider-assignment.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels used by Service and Deployment to match pods.
*/}}
{{- define "rider-assignment.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rider-assignment.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

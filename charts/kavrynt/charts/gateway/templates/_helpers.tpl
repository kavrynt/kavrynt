{{- define "gateway.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gateway.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "gateway.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "gateway.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" -}}
{{- end -}}

{{- define "gateway.labels" -}}
app.kubernetes.io/name: {{ include "gateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ include "gateway.chart" . }}
{{- end -}}

{{- define "gateway.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "gateway.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Comma-separated namespaces the Gateway watches; empty means all. The value is
global so the Operator reads the same list.
*/}}
{{- define "gateway.watchNamespaces" -}}
{{- if .Values.config.watchNamespaces -}}
{{- fail "gateway.config.watchNamespaces was replaced by global.watchNamespaces, which the Operator also reads" -}}
{{- end -}}
{{- join "," ((.Values.global | default dict).watchNamespaces | default list) -}}
{{- end -}}

{{/* "true" when the console admin login is on (SPEC-0005). */}}
{{- define "gateway.consoleAuthEnabled" -}}
{{- $console := .Values.console | default dict -}}
{{- $auth := $console.auth | default dict -}}
{{- if and $console.enabled $auth.enabled -}}true{{- end -}}
{{- end -}}

{{/* The Secret holding the console admin password and session key. */}}
{{- define "gateway.consoleAuthSecret" -}}
{{- $auth := (.Values.console | default dict).auth | default dict -}}
{{- default (printf "%s-console-admin" (include "gateway.fullname" .)) $auth.existingSecret -}}
{{- end -}}

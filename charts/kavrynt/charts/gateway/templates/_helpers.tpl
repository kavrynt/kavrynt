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

{{/*
"true" when MCP client authentication is on. Fails rendering for settings the
Gateway would refuse at start-up (SPEC-0006-R02, SPEC-0006-R20).
*/}}
{{- define "gateway.clientAuthEnabled" -}}
{{- $auth := .Values.auth | default dict -}}
{{- if $auth.enabled -}}
{{- if not $auth.resourceURL -}}
{{- fail "auth.resourceURL is required when auth.enabled is true: the Gateway's external https origin" -}}
{{- end -}}
{{- if not $auth.issuers -}}
{{- fail "auth.issuers needs at least one trusted authorization server when auth.enabled is true" -}}
{{- end -}}
{{- range $auth.issuers -}}
{{- if and (or (hasPrefix "http://" .issuer) (hasPrefix "http://" (.jwksURI | default ""))) (not .allowHTTP) -}}
{{- fail (printf "auth.issuers: %s uses http; set allowHTTP: true for an in-cluster authorization server" .issuer) -}}
{{- end -}}
{{- end -}}
true
{{- end -}}
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

{{/* Pod selector labels. */}}
{{- define "gateway.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* "true" when more than one Gateway replica can run (SPEC-0008-R04). */}}
{{- define "gateway.multiReplica" -}}
{{- if or .Values.autoscaling.enabled (gt (int .Values.replicaCount) 1) -}}true{{- end -}}
{{- end -}}

{{/*
Seconds the kubelet waits after SIGTERM: the drain delay, the shutdown
timeout, and 5 seconds of margin (SPEC-0008-R02).
*/}}
{{- define "gateway.terminationGracePeriodSeconds" -}}
{{- $delay := .Values.config.shutdownDelay | default "5s" | trimSuffix "s" | int -}}
{{- $timeout := .Values.config.shutdownTimeout | default "10s" | trimSuffix "s" | int -}}
{{- add $delay $timeout 5 -}}
{{- end -}}

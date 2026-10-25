{{- /*
kavrynt.notificationReceivers lists the receivers configured under
metrics.notifications (SPEC-0022-R02) as YAML: one AlertmanagerConfig
receiver each, with a severities list for its route. The title and text
templates are read with .Files.Get so Helm leaves their Alertmanager
template syntax alone.
*/}}
{{- define "kavrynt.notificationReceivers" -}}
{{- $n := .Values.metrics.notifications }}
{{- $title := .Files.Get "notifications/title.tmpl" | trim }}
{{- $text := .Files.Get "notifications/text.tmpl" }}
{{- $receivers := list }}
{{- with $n.slack }}
{{- if .webhookSecret.name }}
{{- $config := dict "apiURL" (dict "name" .webhookSecret.name "key" .webhookSecret.key) "sendResolved" true "title" $title "text" $text }}
{{- with .channel }}{{ $_ := set $config "channel" . }}{{ end }}
{{- $receivers = append $receivers (dict "name" "slack" "severities" .severities "slackConfigs" (list $config)) }}
{{- end }}
{{- end }}
{{- with $n.email }}
{{- if .to }}
{{- $config := dict "to" (join ", " .to) "sendResolved" true "requireTLS" .requireTLS "headers" (list (dict "key" "Subject" "value" $title)) "text" $text "html" (printf "<pre>%s</pre>" $text) }}
{{- with .from }}{{ $_ := set $config "from" . }}{{ end }}
{{- with .smarthost }}{{ $_ := set $config "smarthost" . }}{{ end }}
{{- with .authUsername }}{{ $_ := set $config "authUsername" . }}{{ end }}
{{- if .authPasswordSecret.name }}{{ $_ := set $config "authPassword" (dict "name" .authPasswordSecret.name "key" .authPasswordSecret.key) }}{{ end }}
{{- $receivers = append $receivers (dict "name" "email" "severities" .severities "emailConfigs" (list $config)) }}
{{- end }}
{{- end }}
{{- with $n.msteams }}
{{- if .webhookSecret.name }}
{{- $config := dict "webhookURL" (dict "name" .webhookSecret.name "key" .webhookSecret.key) "sendResolved" true "title" $title "text" $text }}
{{- $receivers = append $receivers (dict "name" "msteams" "severities" .severities "msteamsv2Configs" (list $config)) }}
{{- end }}
{{- end }}
{{- with $n.webhook }}
{{- if .urlSecret.name }}
{{- $config := dict "urlSecret" (dict "name" .urlSecret.name "key" .urlSecret.key) "sendResolved" true }}
{{- $receivers = append $receivers (dict "name" "webhook" "severities" .severities "webhookConfigs" (list $config)) }}
{{- end }}
{{- end }}
{{- toYaml (dict "receivers" $receivers) }}
{{- end }}

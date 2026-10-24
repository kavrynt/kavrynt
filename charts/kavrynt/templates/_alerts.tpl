{{- /*
kavrynt.alertRules renders the rule groups of the PrometheusRule
(SPEC-0010). It is a separate helper so the promtool tests in test/alerts
run the same rules. Expressions use only bounded labels: job, instance,
route, method, outcome, namespace, name (SPEC-0010-R06).
*/}}
{{- define "kavrynt.alertRules" -}}
{{- $rule := (.Values.metrics | default dict).prometheusRule | default dict }}
{{- $jobs := $rule.jobs | default dict }}
{{- $gatewayJob := $jobs.gateway | default ((.Values.gateway | default dict).fullnameOverride | default (printf "%s-gateway" .Release.Name)) }}
{{- $operatorJob := $jobs.operator | default (printf "%s-metrics" ((.Values.operator | default dict).fullnameOverride | default (printf "%s-operator" .Release.Name))) }}
{{- $base := $rule.runbookBaseURL | default (printf "https://github.com/kavrynt/kavrynt/blob/v%s/docs/runbooks" .Chart.Version) | trimSuffix "/" }}
{{- $extra := $rule.alertLabels | default dict }}
{{- $cloud := ((.Values.operator | default dict).cloud | default dict).enabled }}
{{- $upstreamFailures := `outcome=~"upstream_error|upstream_unreachable|timeout"` }}
groups:
  - name: kavrynt-gateway
    rules:
      - alert: KavryntGatewayDown
        expr: absent(up{job="{{ $gatewayJob }}"} == 1)
        for: 5m
        labels:
          {{- toYaml (merge (dict "severity" "critical" "component" "gateway") $extra) | nindent 10 }}
        annotations:
          summary: No Kavrynt Gateway replica is up.
          description: Prometheus has scraped no healthy target of job {{ $gatewayJob }} for 5 minutes. MCP clients cannot reach any routed server.
          runbook_url: {{ $base }}/KavryntGatewayDown.md
      - alert: KavryntGatewayRouteSyncFailing
        expr: |-
          sum by (instance) (increase(kavrynt_gateway_route_sync_failure_total{job="{{ $gatewayJob }}"}[10m])) > 0
          unless sum by (instance) (increase(kavrynt_gateway_route_sync_success_total{job="{{ $gatewayJob }}"}[10m])) > 0
        for: 5m
        labels:
          {{- toYaml (merge (dict "severity" "critical" "component" "gateway") $extra) | nindent 10 }}
        annotations:
          summary: Gateway {{ "{{ $labels.instance }}" }} cannot sync its routes.
          description: Route syncs from the Kubernetes API failed for 10 minutes without one success. The replica keeps its last routes, but new, changed, and deleted MCPServers are not applied.
          runbook_url: {{ $base }}/KavryntGatewayRouteSyncFailing.md
      - alert: KavryntMCPUpstreamErrorsHigh
        expr: |-
          (
            sum by (route) (rate(kavrynt_gateway_mcp_requests_total{job="{{ $gatewayJob }}",{{ $upstreamFailures }}}[10m]))
            / sum by (route) (rate(kavrynt_gateway_mcp_requests_total{job="{{ $gatewayJob }}"}[10m]))
          ) > {{ $rule.errorRatio | default 0.05 }}
          and sum by (route) (rate(kavrynt_gateway_mcp_requests_total{job="{{ $gatewayJob }}"}[10m])) >= {{ $rule.minRequestsPerSecond | default 0.05 }}
        for: 5m
        labels:
          {{- toYaml (merge (dict "severity" "warning" "component" "mcp-server") $extra) | nindent 10 }}
        annotations:
          summary: MCP route {{ "{{ $labels.route }}" }} fails upstream.
          description: '{{ "{{ $value | humanizePercentage }}" }} of calls to {{ "{{ $labels.route }}" }} ended in upstream_error, upstream_unreachable, or timeout over 10 minutes.'
          runbook_url: {{ $base }}/KavryntMCPUpstreamErrorsHigh.md
      - alert: KavryntMCPLatencyHigh
        expr: |-
          histogram_quantile(0.95, sum by (route, le) (rate(kavrynt_gateway_mcp_request_duration_seconds_bucket{job="{{ $gatewayJob }}"}[10m])))
          > {{ $rule.latencyP95Seconds | default 5 }}
        for: 15m
        labels:
          {{- toYaml (merge (dict "severity" "warning" "component" "mcp-server") $extra) | nindent 10 }}
        annotations:
          summary: MCP route {{ "{{ $labels.route }}" }} is slow.
          description: The p95 latency of calls to {{ "{{ $labels.route }}" }} has been {{ "{{ $value | humanizeDuration }}" }} for 15 minutes.
          runbook_url: {{ $base }}/KavryntMCPLatencyHigh.md
  - name: kavrynt-operator
    rules:
      - alert: KavryntOperatorDown
        expr: absent(up{job="{{ $operatorJob }}"} == 1)
        for: 5m
        labels:
          {{- toYaml (merge (dict "severity" "critical" "component" "operator") $extra) | nindent 10 }}
        annotations:
          summary: No Kavrynt Operator replica is up.
          description: Prometheus has scraped no healthy target of job {{ $operatorJob }} for 5 minutes. New and changed MCPServers are not validated, so the Gateway does not route them.
          runbook_url: {{ $base }}/KavryntOperatorDown.md
      - alert: KavryntMCPServerNotReady
        expr: max by (namespace, name) (kavrynt_operator_mcpserver_ready{job="{{ $operatorJob }}"}) == 0
        for: {{ $rule.notReadyFor | default "10m" }}
        labels:
          {{- toYaml (merge (dict "severity" "warning" "component" "mcp-server") $extra) | nindent 10 }}
        annotations:
          summary: MCPServer {{ "{{ $labels.namespace }}/{{ $labels.name }}" }} is not Ready.
          description: The Operator has reported {{ "{{ $labels.namespace }}/{{ $labels.name }}" }} as not Ready for {{ $rule.notReadyFor | default "10m" }}; the Gateway does not route it.
          runbook_url: {{ $base }}/KavryntMCPServerNotReady.md
      {{- if $cloud }}
      - alert: KavryntCloudSyncStale
        expr: time() - max(kavrynt_operator_cloud_sync_last_success_timestamp_seconds{job="{{ $operatorJob }}"}) > {{ $rule.cloudSyncStaleSeconds | default 600 }}
        for: 5m
        labels:
          {{- toYaml (merge (dict "severity" "warning" "component" "operator") $extra) | nindent 10 }}
        annotations:
          summary: The Operator has not synced to Kavrynt Cloud.
          description: Kavrynt Cloud last accepted an inventory snapshot {{ "{{ $value | humanizeDuration }}" }} ago.
          runbook_url: {{ $base }}/KavryntCloudSyncStale.md
      {{- end }}
{{- end }}

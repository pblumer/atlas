{{/*
Expand the name of the chart.
*/}}
{{- define "atlas.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name. Truncated to 63 chars for the Kubernetes name limit.
*/}}
{{- define "atlas.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version, for the chart label.
*/}}
{{- define "atlas.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "atlas.labels" -}}
helm.sh/chart: {{ include "atlas.chart" . }}
{{ include "atlas.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels — stable across upgrades, so they must not include version.
*/}}
{{- define "atlas.selectorLabels" -}}
app.kubernetes.io/name: {{ include "atlas.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name to use.
*/}}
{{- define "atlas.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "atlas.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image reference: repository:tag, defaulting the tag to appVersion.
*/}}
{{- define "atlas.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Name of the chart-managed secret holding admin/vault material.
*/}}
{{- define "atlas.secretName" -}}
{{- printf "%s-secrets" (include "atlas.fullname" .) }}
{{- end }}

{{/*
Whether the chart needs to render its own Secret (i.e. a value was supplied
inline rather than via an existing secret).
*/}}
{{- define "atlas.createSecret" -}}
{{- $auth := .Values.atlas.auth -}}
{{- $vault := .Values.atlas.vault -}}
{{- if and $auth.enabled (not $auth.existingSecret) $auth.password -}}
true
{{- else if and $vault.enabled (not $vault.existingSecret) $vault.key -}}
true
{{- end -}}
{{- end }}

{{/*
atlas.probe renders one probe, forcing the HTTPS scheme where the server
terminates TLS itself: the kubelet would otherwise speak plaintext to a TLS
listener and the pod would never become ready. Its plaintext loopback listener is
no help here — that one is bound on 127.0.0.1 by design (ADR-0191). A probe does
not verify the certificate, so an internally issued one needs nothing further.
*/}}
{{- define "atlas.probe" -}}
{{- $probe := deepCopy .probe -}}
{{- if and .tls (hasKey $probe "httpGet") -}}
{{- $_ := set $probe.httpGet "scheme" "HTTPS" -}}
{{- end -}}
{{- toYaml $probe -}}
{{- end -}}

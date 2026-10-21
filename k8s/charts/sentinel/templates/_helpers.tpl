{{/*
Expand the name of the chart.
*/}}
{{- define "sentinel.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "sentinel.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "sentinel.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Labels and image helpers below take a dict, not the root context:
  include "sentinel.labels" (dict "ctx" $ "component" "api-gateway")
"component" is the workload's name (the app.kubernetes.io/name label and the
name its selector matches); the optional "tier" adds app.kubernetes.io/component
(api, ingest, gis).
*/}}

{{/*
Selector labels. Keep these exactly as they are: a Deployment's selector is
immutable, so a change here would make every upgrade fail.
*/}}
{{- define "sentinel.selectorLabels" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
{{- end }}

{{/*
Common labels, for the metadata of a resource.
*/}}
{{- define "sentinel.labels" -}}
helm.sh/chart: {{ include "sentinel.chart" .ctx }}
{{ include "sentinel.selectorLabels" . }}
app.kubernetes.io/part-of: sentinel
app.kubernetes.io/version: {{ .ctx.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .ctx.Release.Service }}
{{- with .tier }}
app.kubernetes.io/component: {{ . }}
{{- end }}
{{- end }}

{{/*
Pod template labels: the selector labels and part-of, but not the chart version,
so a chart bump alone does not restart the pods.
*/}}
{{- define "sentinel.podLabels" -}}
{{ include "sentinel.selectorLabels" . }}
app.kubernetes.io/part-of: sentinel
{{- end }}

{{/*
Image of a Sentinel service: global.imageRegistry/repository:tag, the tag falling
back to global.imageTag, then the chart's appVersion.
  include "sentinel.image" (dict "ctx" $ "image" .Values.apiGateway.image)
Third-party images (postgres, geoserver, curl) are pulled from their own
registries and do not use this.
*/}}
{{- define "sentinel.image" -}}
{{- $g := .ctx.Values.global -}}
{{- $tag := .image.tag | default $g.imageTag | default .ctx.Chart.AppVersion -}}
{{- if $g.imageRegistry -}}
{{ trimSuffix "/" $g.imageRegistry }}/{{ .image.repository }}:{{ $tag }}
{{- else -}}
{{ .image.repository }}:{{ $tag }}
{{- end -}}
{{- end }}

{{/*
The imagePullSecrets of a pod spec, from global.imagePullSecrets (empty when
there are none), used as
  {{- with (include "sentinel.imagePullSecrets" .) }}
  {{- . | nindent 6 }}
  {{- end }}
*/}}
{{- define "sentinel.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets -}}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{/*
A generated secret value that survives upgrades: the value of key in Secret
name when the release's namespace already has one, else 32 random characters.
  include "sentinel.secretValue" (dict "ctx" $ "name" (printf "%s-x" (include "sentinel.fullname" $)) "key" "password")
Every call without an existing Secret returns a different value, so render a
value in one place only (the Secret) and let other templates reference it with
secretKeyRef. Under helm template, lookup finds nothing: the value is random.
*/}}
{{- define "sentinel.secretValue" -}}
{{- $existing := lookup "v1" "Secret" .ctx.Release.Namespace .name -}}
{{- if and $existing $existing.data (hasKey $existing.data .key) -}}
{{- index $existing.data .key | b64dec -}}
{{- else -}}
{{- randAlphaNum 32 -}}
{{- end -}}
{{- end }}

{{/*
Public hostname of the deployment, as a URL (global.host has no scheme). For
CORS origins, token issuers and the Ingress.
*/}}
{{- define "sentinel.publicUrl" -}}
https://{{ required "global.host is required" .Values.global.host }}
{{- end }}

{{/*
In-cluster addresses of the data tier, which the chart deploys.
*/}}
{{- define "sentinel.postgresHost" -}}
{{ include "sentinel.fullname" . }}-postgresql
{{- end }}

{{- define "sentinel.kafkaBrokers" -}}
{{ include "sentinel.fullname" . }}-kafka:9092
{{- end }}

{{- define "sentinel.keycloakInternalUrl" -}}
http://{{ include "sentinel.fullname" . }}-keycloak:8080/auth
{{- end }}

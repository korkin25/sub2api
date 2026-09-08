{{/* Chart name, overridable. */}}
{{- define "sub2api.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified release name. */}}
{{- define "sub2api.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "sub2api.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "sub2api.labels" -}}
helm.sh/chart: {{ include "sub2api.chart" . }}
{{ include "sub2api.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: sub2api
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "sub2api.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sub2api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sub2api.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "sub2api.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Image reference. A digest wins over a tag; an empty tag falls back to
.Chart.AppVersion so a plain `helm install` pulls a matching image.
*/}}
{{- define "sub2api.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag | toString) -}}
{{- end -}}
{{- end -}}

{{- define "sub2api.redis.image" -}}
{{- if .Values.redis.image.digest -}}
{{- printf "%s@%s" .Values.redis.image.repository .Values.redis.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.redis.image.repository (.Values.redis.image.tag | toString) -}}
{{- end -}}
{{- end -}}

{{- define "sub2api.redis.fullname" -}}
{{- printf "%s-redis" (include "sub2api.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Host the application dials for Redis: the bundled one, or the external one. */}}
{{- define "sub2api.redis.host" -}}
{{- if .Values.redis.enabled -}}
{{- include "sub2api.redis.fullname" . -}}
{{- else -}}
{{- required "sub2api: set redis.host when redis.enabled is false" .Values.redis.host -}}
{{- end -}}
{{- end -}}

{{/* Name of the Secret the workload reads credentials from. */}}
{{- define "sub2api.secretName" -}}
{{- if .Values.auth.existingSecret -}}
{{- .Values.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-auth" (include "sub2api.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "sub2api.createSecret" -}}
{{- if .Values.auth.existingSecret -}}false{{- else -}}true{{- end -}}
{{- end -}}

{{/* PVC the data volume binds to. */}}
{{- define "sub2api.dataClaimName" -}}
{{- if .Values.persistence.existingClaim -}}
{{- .Values.persistence.existingClaim -}}
{{- else -}}
{{- printf "%s-data" (include "sub2api.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Fail fast, with an actionable message, on the values a working release cannot
do without. Called once from the Deployment.
*/}}
{{- define "sub2api.validateValues" -}}
{{- if not .Values.postgresql.host -}}
{{- fail "sub2api: postgresql.host is required — this chart does not ship a database, point it at your PostgreSQL 14+ instance" -}}
{{- end -}}
{{- if not .Values.auth.existingSecret -}}
{{- if not .Values.auth.databasePassword -}}
{{- fail "sub2api: set auth.databasePassword, or point auth.existingSecret at a Secret that carries it" -}}
{{- end -}}
{{- if not .Values.auth.jwtSecret -}}
{{- fail "sub2api: set auth.jwtSecret (openssl rand -hex 32), or point auth.existingSecret at a Secret that carries it. Leaving it unset makes the application generate a new secret on every restart, logging every user out" -}}
{{- end -}}
{{- if not .Values.auth.totpEncryptionKey -}}
{{- fail "sub2api: set auth.totpEncryptionKey (openssl rand -hex 32), or point auth.existingSecret at a Secret that carries it" -}}
{{- end -}}
{{- if not (regexMatch "^[0-9a-fA-F]{64}$" .Values.auth.totpEncryptionKey) -}}
{{- fail "sub2api: auth.totpEncryptionKey must be a 32-byte AES key, hex-encoded — exactly 64 hex characters (openssl rand -hex 32)" -}}
{{- end -}}
{{- if and .Values.redis.enabled (not .Values.auth.redisPassword) -}}
{{- fail "sub2api: the bundled Redis requires auth.redisPassword — set it, disable it with redis.enabled=false, or supply auth.existingSecret" -}}
{{- end -}}
{{- end -}}
{{- end -}}

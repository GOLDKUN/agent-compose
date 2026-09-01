{{/* Expand the chart name. */}}
{{- define "agent-compose.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Create a release-scoped name for namespaced resources. */}}
{{- define "agent-compose.fullname" -}}
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

{{/* Common labels. */}}
{{- define "agent-compose.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "agent-compose.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Selector labels. */}}
{{- define "agent-compose.selectorLabels" -}}
app.kubernetes.io/name: {{ include "agent-compose.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* The namespace in which default sandbox Pods are created. */}}
{{- define "agent-compose.sandboxNamespace" -}}
{{- default .Release.Namespace .Values.runtime.sandboxNamespace -}}
{{- end }}

{{/* The daemon URL reachable from an in-cluster sandbox Pod. */}}
{{- define "agent-compose.runtimeBaseURL" -}}
{{- default (printf "http://%s.%s.svc.cluster.local:%d" (include "agent-compose.fullname" .) .Release.Namespace (int .Values.service.port)) .Values.runtime.runtimeBaseURL -}}
{{- end }}

{{/* Cluster-scoped names include the release namespace so two releases in
     different namespaces do not share a ClusterRoleBinding subject. */}}
{{- define "agent-compose.clusterRoleName" -}}
{{- $base := include "agent-compose.fullname" . | trunc 54 | trimSuffix "-" -}}
{{- printf "%s-%s" $base (sha256sum .Release.Namespace | trunc 8) | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/* ServiceAccount name. */}}
{{- define "agent-compose.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- include "agent-compose.fullname" . }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* Daemon data PVC name. */}}
{{- define "agent-compose.dataClaimName" -}}
{{- default (printf "%s-data" (include "agent-compose.fullname" .)) .Values.persistence.existingClaim -}}
{{- end }}

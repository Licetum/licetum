{{/* The name of the release objects. */}}
{{- define "licetum.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "licetum.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.region }}
licetum.io/region: {{ . | quote }}
{{- end }}
{{- end -}}

{{/* selectorLabels: pass (dict "root" . "component" "check"). */}}
{{- define "licetum.selectorLabels" -}}
app.kubernetes.io/name: {{ .root.Chart.Name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "licetum.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "licetum.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* The Secret that holds the keys: the one the release names, or the one it creates. */}}
{{- define "licetum.secretName" -}}
{{- default (printf "%s-secrets" (include "licetum.fullname" .)) .Values.existingSecret -}}
{{- end -}}

{{- define "licetum.imageCore" -}}
{{- printf "%s:%s" .Values.image.core.repository (default .Chart.AppVersion .Values.image.core.tag) -}}
{{- end -}}
{{- define "licetum.imageAdmin" -}}
{{- printf "%s:%s" .Values.image.admin.repository (default .Chart.AppVersion .Values.image.admin.tag) -}}
{{- end -}}
{{- define "licetum.imageWeb" -}}
{{- printf "%s:%s" .Values.image.web.repository (default .Chart.AppVersion .Values.image.web.tag) -}}
{{- end -}}

{{/* One variable of the Secret. Pass (dict "root" . "key" "DB__URI" "optional" false). */}}
{{- define "licetum.secretEnv" -}}
- name: {{ .key }}
  valueFrom:
    secretKeyRef:
      name: {{ include "licetum.secretName" .root }}
      key: {{ .key }}
      {{- if .optional }}
      optional: true
      {{- end }}
{{- end -}}

{{/* The time a pod needs to go away: the preStop sleep, the drain of the process, the wait for the requests, and a margin. */}}
{{- define "licetum.terminationGrace" -}}
{{- add (mul 2 (int .Values.shutdown.drain)) (int .Values.shutdown.timeout) 10 -}}
{{- end -}}

{{/* Placement. Pass (dict "root" . "component" "check"). */}}
{{- define "licetum.placement" -}}
{{- if .root.Values.topologySpreadConstraints.enabled }}
topologySpreadConstraints:
  - maxSkew: {{ .root.Values.topologySpreadConstraints.maxSkew }}
    topologyKey: {{ .root.Values.topologySpreadConstraints.topologyKey }}
    whenUnsatisfiable: {{ .root.Values.topologySpreadConstraints.whenUnsatisfiable }}
    labelSelector:
      matchLabels:
        {{- include "licetum.selectorLabels" . | nindent 8 }}
{{- end }}
{{- if .root.Values.podAntiAffinity.enabled }}
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          topologyKey: {{ .root.Values.podAntiAffinity.topologyKey }}
          labelSelector:
            matchLabels:
              {{- include "licetum.selectorLabels" . | nindent 14 }}
{{- end }}
{{- end -}}

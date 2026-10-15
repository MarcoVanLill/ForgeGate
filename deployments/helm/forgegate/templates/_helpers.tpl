{{- define "forgegate.name" -}}forgegate{{- end }}
{{- define "forgegate.fullname" -}}{{ .Release.Name }}-forgegate{{- end }}
{{- define "forgegate.labels" -}}
app.kubernetes.io/name: {{ include "forgegate.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}
{{- define "forgegate.serviceAccountName" -}}{{ default (include "forgegate.fullname" .) .Values.serviceAccount.name }}{{- end }}

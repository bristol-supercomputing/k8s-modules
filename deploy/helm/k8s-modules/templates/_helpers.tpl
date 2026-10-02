{{- define "k8s-modules.labels" -}}
app.kubernetes.io/name: module-controller
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: k8s-modules
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" | quote }}
{{- end }}
{{- define "k8s-modules.selector" -}}
app.kubernetes.io/name: module-controller
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "k8s-modules.podPolicyNamespaces" -}}
namespaceSelector:
  matchExpressions:
  - {key: kubernetes.io/metadata.name, operator: NotIn, values: [kube-system]}
{{- end }}

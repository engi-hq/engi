{{- define "engi.fullname" -}}
{{- if .Values.fullnameOverride -}}{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}{{- $n := default .Chart.Name .Values.nameOverride -}}
{{- if contains $n .Release.Name -}}{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}{{- printf "%s-%s" .Release.Name $n | trunc 63 | trimSuffix "-" -}}{{- end -}}
{{- end -}}
{{- end -}}

{{- define "engi.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
{{- end -}}

{{- define "engi.roleLabels" -}}
app.kubernetes.io/name: {{ .root.Chart.Name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .role }}
{{- end -}}

{{- define "engi.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{/* standalone or cluster: the explicit mode, else cluster when a Postgres is configured. */}}
{{- define "engi.mode" -}}
{{- if .Values.mode -}}{{ .Values.mode }}
{{- else if or .Values.postgres.host .Values.postgres.existingSecret -}}cluster
{{- else -}}standalone{{- end -}}
{{- end -}}

{{- define "engi.isCluster" -}}{{ if eq (include "engi.mode" .) "cluster" }}true{{ end }}{{- end -}}

{{/* Comma-separated roles that get a Deployment: "standalone", or the enabled cluster roles. */}}
{{- define "engi.roleList" -}}
{{- if include "engi.isCluster" . -}}
{{- $out := list -}}
{{- range $role := list "api" "webhook" "trigger" "worker" -}}
{{- if (index $.Values.roles $role).enabled -}}{{- $out = append $out $role -}}{{- end -}}
{{- end -}}
{{- join "," $out -}}
{{- else -}}standalone{{- end -}}
{{- end -}}

{{/* The pod that serves a Service: the role itself in cluster mode, the single pod otherwise. */}}
{{- define "engi.servingRole" -}}
{{- if include "engi.isCluster" .root -}}{{ .role }}{{- else -}}standalone{{- end -}}
{{- end -}}

{{- define "engi.blobDriver" -}}
{{- if .Values.blobs.driver -}}{{ .Values.blobs.driver }}
{{- else if include "engi.isCluster" . -}}s3
{{- else -}}fs{{- end -}}
{{- end -}}

{{- define "engi.encryptionSecretName" -}}
{{- default (printf "%s-encryption" (include "engi.fullname" .)) .Values.encryption.existingSecret -}}
{{- end -}}

{{/* Postgres connection string without the password (that comes as PGPASSWORD). */}}
{{- define "engi.postgresDSN" -}}
{{- $p := .Values.postgres -}}
{{- printf "postgres://%s@%s:%d/%s?sslmode=%s" (urlquery $p.user) $p.host (int $p.port) (urlquery $p.database) $p.sslmode -}}
{{- end -}}

{{/* Fails the render with a clear message when the values cannot work. */}}
{{- define "engi.validate" -}}
{{- $mode := include "engi.mode" . -}}
{{- if not (has $mode (list "standalone" "cluster")) -}}
{{- fail (printf "mode must be standalone or cluster, not %q" $mode) -}}
{{- end -}}
{{- $p := .Values.postgres -}}
{{- if and $p.host $p.existingSecret -}}
{{- fail "set either postgres.host (with postgres.passwordSecret) or postgres.existingSecret, not both" -}}
{{- end -}}
{{- if and (eq $mode "cluster") (not (or $p.host $p.existingSecret)) -}}
{{- fail "cluster mode needs your Postgres: set postgres.host and postgres.passwordSecret.name, or postgres.existingSecret" -}}
{{- end -}}
{{- if and (eq $mode "standalone") (or $p.host $p.existingSecret) -}}
{{- fail "standalone mode keeps its data in SQLite; remove postgres.* or use mode: cluster" -}}
{{- end -}}
{{- $driver := include "engi.blobDriver" . -}}
{{- if not (has $driver (list "fs" "s3")) -}}
{{- fail (printf "blobs.driver must be fs or s3, not %q" $driver) -}}
{{- end -}}
{{- if and (eq $mode "cluster") (eq $driver "fs") -}}
{{- fail "cluster mode runs several pods that do not share a filesystem: use blobs.driver s3 with blobs.s3.bucket and blobs.s3.region" -}}
{{- end -}}
{{- if and (eq $driver "s3") (not (and .Values.blobs.s3.bucket .Values.blobs.s3.region)) -}}
{{- fail "blobs.driver s3 needs blobs.s3.bucket and blobs.s3.region (any value works for MinIO, e.g. us-east-1)" -}}
{{- end -}}
{{- if and (eq $mode "standalone") (eq $driver "fs") (not .Values.standalone.persistence.enabled) -}}
{{- fail "standalone mode without persistence loses every workflow on restart: set standalone.persistence.enabled true (or existingClaim)" -}}
{{- end -}}
{{- end -}}

{{- define "engi.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}{{ include "engi.fullname" . }}{{- else -}}default{{- end -}}
{{- end -}}

{{/* Trigger pods use their own ServiceAccount when they need the Kubernetes API (Lease). */}}
{{- define "engi.triggerServiceAccountName" -}}
{{- if eq .Values.lease.type "kubernetes" -}}{{ include "engi.fullname" . }}-trigger{{- else -}}{{ include "engi.serviceAccountName" . }}{{- end -}}
{{- end -}}

{{/* Environment shared by every Engi container: store, blobs, secrets. */}}
{{- define "engi.storeEnv" -}}
- name: ENGI_ENCRYPTION_KEYS_FILE
  value: {{ printf "%s/encryption-keys" .Values.secretsMountPath }}
- name: ENGI_ENCRYPTION_ACTIVE_KEY_ID
  value: {{ .Values.encryption.activeKeyId | quote }}
{{- if include "engi.isCluster" . }}
- {name: ENGI_STORE_DRIVER, value: postgres}
{{- if .Values.postgres.existingSecret }}
- name: ENGI_STORE_POSTGRES_DSN_FILE
  value: {{ printf "%s/postgres-dsn" .Values.secretsMountPath }}
{{- else }}
- name: ENGI_STORE_POSTGRES_DSN
  value: {{ include "engi.postgresDSN" . | quote }}
{{- with .Values.postgres.passwordSecret.name }}
- name: PGPASSWORD
  valueFrom: {secretKeyRef: {name: {{ . }}, key: {{ $.Values.postgres.passwordSecret.key }}}}
{{- end }}
{{- end }}
{{- else }}
- {name: ENGI_STORE_DRIVER, value: sqlite}
- {name: ENGI_STORE_PATH, value: /data/engi.db}
{{- end }}
{{- $s3 := .Values.blobs.s3 }}
- name: ENGI_BLOB_DRIVER
  value: {{ include "engi.blobDriver" . }}
{{- if eq (include "engi.blobDriver" .) "s3" }}
- {name: ENGI_BLOB_S3_BUCKET, value: {{ $s3.bucket | quote }}}
- {name: ENGI_BLOB_S3_REGION, value: {{ $s3.region | quote }}}
{{- with $s3.endpoint }}
- {name: ENGI_BLOB_S3_ENDPOINT, value: {{ . | quote }}}
{{- end }}
- {name: ENGI_BLOB_S3_FORCE_PATH_STYLE, value: {{ $s3.forcePathStyle | quote }}}
{{- with $s3.prefix }}
- {name: ENGI_BLOB_S3_PREFIX, value: {{ . | quote }}}
{{- end }}
{{- if $s3.existingSecret }}
- name: ENGI_BLOB_S3_ACCESS_KEY_ID
  valueFrom: {secretKeyRef: {name: {{ $s3.existingSecret }}, key: {{ $s3.accessKeyIdKey }}}}
- name: ENGI_BLOB_S3_SECRET_ACCESS_KEY_FILE
  value: {{ printf "%s/s3-secret-access-key" .Values.secretsMountPath }}
{{- end }}
{{- else }}
- {name: ENGI_BLOB_FS_DIR, value: /data/blobs}
{{- end }}
{{- end -}}

{{/* One projected volume carries every secret file; the data volume is the PVC in standalone mode. */}}
{{- define "engi.volumes" -}}
- name: secrets
  projected:
    defaultMode: 0440
    sources:
      - secret:
          name: {{ include "engi.encryptionSecretName" . }}
          items: [{key: {{ .Values.encryption.key }}, path: encryption-keys}]
      {{- if and (include "engi.isCluster" .) .Values.postgres.existingSecret }}
      - secret:
          name: {{ .Values.postgres.existingSecret }}
          items: [{key: {{ .Values.postgres.dsnKey }}, path: postgres-dsn}]
      {{- end }}
      {{- if and (eq (include "engi.blobDriver" .) "s3") .Values.blobs.s3.existingSecret }}
      - secret:
          name: {{ .Values.blobs.s3.existingSecret }}
          items: [{key: {{ .Values.blobs.s3.secretAccessKeyKey }}, path: s3-secret-access-key}]
      {{- end }}
- name: data
  {{- if and (not (include "engi.isCluster" .)) .Values.standalone.persistence.enabled }}
  persistentVolumeClaim:
    claimName: {{ default (printf "%s-data" (include "engi.fullname" .)) .Values.standalone.persistence.existingClaim }}
  {{- else }}
  emptyDir: {}
  {{- end }}
- name: tmp
  emptyDir: {}
{{- end -}}

{{- define "engi.volumeMounts" -}}
- {name: secrets, mountPath: {{ .Values.secretsMountPath }}, readOnly: true}
- {name: data, mountPath: /data}
- {name: tmp, mountPath: /tmp}
{{- end -}}

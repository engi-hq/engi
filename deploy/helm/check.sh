#!/usr/bin/env bash
# Renders the chart for every supported setup and checks what each produces,
# and that unworkable values fail with a clear message. Run: make helm-check
set -euo pipefail

chart="$(cd "$(dirname "$0")/engi" && pwd)"
failures=0

render() { helm template t "$chart" "$@"; }

expect() { # expect <name> <pattern> <helm args...>: the rendered output matches pattern
	local name=$1 pattern=$2; shift 2
	if render "$@" | grep -Eq -- "$pattern"; then echo "ok   $name"; else echo "FAIL $name: no match for /$pattern/"; failures=$((failures + 1)); fi
}

expect_not() { # expect_not <name> <pattern> <helm args...>
	local name=$1 pattern=$2; shift 2
	if render "$@" | grep -Eq -- "$pattern"; then echo "FAIL $name: unexpected /$pattern/"; failures=$((failures + 1)); else echo "ok   $name"; fi
}

expect_fail() { # expect_fail <name> <message part> <helm args...>
	local name=$1 message=$2; shift 2
	local out
	if out=$(render "$@" 2>&1); then echo "FAIL $name: rendered, want an error"; failures=$((failures + 1));
	elif grep -Fq -- "$message" <<<"$out"; then echo "ok   $name"; else echo "FAIL $name: error lacks '$message': $out"; failures=$((failures + 1)); fi
}

count() { render "$@" | grep -c "^kind: Deployment" || true; }

cluster=(--set postgres.host=pg.db.svc --set postgres.passwordSecret.name=pg-app
	--set blobs.s3.bucket=engi --set blobs.s3.region=us-east-1)

# Standalone: the default, needs nothing.
[[ $(count) == 1 ]] && echo "ok   standalone has one Deployment" || { echo "FAIL standalone Deployment count"; failures=$((failures + 1)); }
expect "standalone uses SQLite on the volume" 'value: sqlite'
expect "standalone creates its PVC" 'kind: PersistentVolumeClaim'
expect "standalone recreates, never two pods" 'type: Recreate'
expect "standalone generates the encryption key" 'name: t-engi-encryption'
expect_not "standalone has no migrate Job" 'name: t-engi-migrate'
expect "standalone uses an existing claim" 'claimName: mine' --set standalone.persistence.existingClaim=mine
expect_not "an existing claim is not created" 'kind: PersistentVolumeClaim' --set standalone.persistence.existingClaim=mine

expect "public URL reaches the server" 'ENGI_SERVER_PUBLIC_URL: "https://engi.example.com"' --set publicUrl=https://engi.example.com
expect_not "public URL is left to the binary default when empty" 'ENGI_SERVER_PUBLIC_URL' 

# Cluster with Postgres parts and the password from the user's Secret.
[[ $(count "${cluster[@]}") == 4 ]] && echo "ok   cluster has four Deployments" || { echo "FAIL cluster Deployment count"; failures=$((failures + 1)); }
expect "cluster DSN carries no password" 'postgres://engi@pg.db.svc:5432/engi\?sslmode=require' "${cluster[@]}"
expect "cluster reads the password from the user's Secret" 'name: pg-app, key: password' "${cluster[@]}"
expect "cluster migrates in a hook" 'name: t-engi-migrate' "${cluster[@]}"
expect "cluster blobs go to S3" 'ENGI_BLOB_S3_BUCKET, value: "engi"' "${cluster[@]}"
expect_not "cluster creates no PVC" 'kind: PersistentVolumeClaim' "${cluster[@]}"
expect "S3 keys come from the user's Secret" 'secret-access-key, path: s3-secret-access-key' "${cluster[@]}" --set blobs.s3.existingSecret=s3-creds
expect "S3 without keys mounts the token for workload identity" 'automountServiceAccountToken: true' "${cluster[@]}"
expect "MinIO endpoint and path style" 'ENGI_BLOB_S3_FORCE_PATH_STYLE, value: "true"' "${cluster[@]}" --set blobs.s3.endpoint=http://minio:9000 --set blobs.s3.forcePathStyle=true

# Cluster with a full DSN in the user's Secret.
dsn=(--set postgres.existingSecret=engi-db --set blobs.s3.bucket=engi --set blobs.s3.region=us-east-1)
expect "DSN Secret is mounted as a file" 'path: postgres-dsn' "${dsn[@]}"
expect "DSN Secret is read from the file" 'ENGI_STORE_POSTGRES_DSN_FILE' "${dsn[@]}"
expect_not "user-managed encryption key is not generated" 'name: t-engi-encryption$' "${dsn[@]}" --set encryption.existingSecret=my-keys

# Unworkable values fail before anything is applied.
expect_fail "cluster without Postgres" "cluster mode needs your Postgres" --set mode=cluster
expect_fail "cluster with filesystem blobs" "do not share a filesystem" "${cluster[@]}" --set blobs.driver=fs
expect_fail "S3 without a bucket" "needs blobs.s3.bucket" --set postgres.host=pg --set blobs.s3.region=x
expect_fail "both Postgres styles" "not both" "${cluster[@]}" --set postgres.existingSecret=x
expect_fail "standalone with Postgres" "standalone mode keeps its data in SQLite" "${cluster[@]}" --set mode=standalone
expect_fail "standalone without persistence" "loses every workflow" --set standalone.persistence.enabled=false
expect_fail "unknown mode" "mode must be standalone or cluster" --set mode=huge

helm lint --quiet "$chart" >/dev/null
echo "ok   helm lint"
if ((failures > 0)); then echo "$failures check(s) failed"; exit 1; fi

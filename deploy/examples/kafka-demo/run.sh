#!/usr/bin/env bash
# Kafka demo against a running Engi: create a Kafka credential and the
# workflow.json workflow, activate it, send one order with producer.yaml and
# wait for the execution to finish. Needs curl, python3 and kubectl.
#
#   kubectl port-forward -n engi svc/engi-api 8081:8080 &
#   deploy/examples/kafka-demo/run.sh http://localhost:8081
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
base=${1:-http://localhost:8081}
brokers=${KAFKA_BROKERS:-kafka.infra.svc.cluster.local:9092}
email=${ENGI_EMAIL:-smoke@example.com}
password=${ENGI_PASSWORD:-smoke-test-password-1}
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT

json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
call() { # call <method> <path> [body]: prints the body, fails on a non-2xx status
	local out status
	out=$(curl -sS -b "$jar" -c "$jar" -X "$1" -H "Content-Type: application/json" ${csrf:+-H "X-CSRF-Token: $csrf"} \
		-w '\n%{http_code}' "$base$2" ${3:+--data "$3"})
	status=${out##*$'\n'}
	out=${out%$'\n'*}
	if [[ $status != 2* ]]; then echo "$1 $2 -> $status: $out" >&2; return 1; fi
	printf '%s' "$out"
}

csrf=""
if [[ $(call GET /api/v1/auth/setup | json 'd.get("required", False)') == True ]]; then
	call POST /api/v1/auth/setup "{\"email\":\"$email\",\"name\":\"Demo\",\"password\":\"$password\"}" >/dev/null
	echo "owner created: $email"
fi
csrf=$(call POST /api/v1/auth/login "{\"email\":\"$email\",\"password\":\"$password\"}" | json 'd["csrfToken"]')

cred=$(call POST /api/v1/credentials "{\"name\":\"Demo Kafka\",\"type\":\"engi.kafka\",\"data\":{\"brokers\":\"$brokers\"}}" | json 'd["id"]')
wf=$(sed "s/CREDENTIAL_ID/$cred/" "$here/workflow.json")
id=$(call POST /api/v1/workflows "$wf" | json 'd["id"]')
call POST "/api/v1/workflows/$id/activate" "{}" >/dev/null
echo "workflow $id active, listening on topic orders"

kubectl delete job order-producer -n infra --ignore-not-found >/dev/null
kubectl apply -f "$here/producer.yaml" >/dev/null
kubectl wait --for=condition=complete job/order-producer -n infra --timeout=120s >/dev/null
kubectl logs job/order-producer -n infra | tail -1

for _ in $(seq 30); do
	ex=$(call GET "/api/v1/executions?workflowId=$id" | json 'next((e["id"]+" "+e["status"] for e in d["data"]), "")')
	[[ $ex == *" success" || $ex == *" error" ]] && break
	sleep 2
done
echo "execution: ${ex:-none yet}"
[[ $ex == *" success" ]] || exit 1
echo "Total node output: $(call GET "/api/v1/executions/${ex%% *}" | json 'json.dumps(d["runData"]["Total"][0]["output"]["main"][0][0]["json"])')"

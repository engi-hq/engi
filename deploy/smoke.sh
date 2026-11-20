#!/usr/bin/env bash
# End-to-end check of a running Engi: create the owner (or log in), save a
# webhook workflow, activate it and call it. Works against any deployment
# (Docker, Helm, Swarm) reachable at BASE_URL. Needs curl and python3.
#
#   deploy/smoke.sh http://localhost:8080
#   deploy/smoke.sh http://api:8080 http://webhook:8080   # roles split (cluster mode)
set -euo pipefail

base=${1:-http://localhost:8080}
hooks=${2:-$base}
email=${ENGI_SMOKE_EMAIL:-smoke@example.com}
password=${ENGI_SMOKE_PASSWORD:-smoke-test-password-1}
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT

json() { python3 -c "import json,sys; print(json.load(sys.stdin)$1)"; }
call() { # call <method> <path> [body]: prints the body, fails on a non-2xx status
	local out status
	out=$(curl -sS -b "$jar" -c "$jar" -X "$1" -H "Content-Type: application/json" ${csrf:+-H "X-CSRF-Token: $csrf"} \
		-w '\n%{http_code}' "$base$2" ${3:+--data "$3"})
	status=${out##*$'\n'}
	out=${out%$'\n'*}
	if [[ $status != 2* ]]; then echo "$1 $2 -> $status: $out" >&2; return 1; fi
	printf '%s' "$out"
}

echo "waiting for $base/readyz"
for _ in $(seq 60); do curl -fsS "$base/readyz" >/dev/null 2>&1 && break; sleep 2; done
curl -fsS "$base/readyz" >/dev/null

csrf=""
creds="{\"email\":\"$email\",\"password\":\"$password\"}"
if [[ $(call GET /api/v1/auth/setup | json '.get("required", False)') == True ]]; then
	call POST /api/v1/auth/setup "{\"email\":\"$email\",\"name\":\"Smoke\",\"password\":\"$password\"}" >/dev/null
	echo "owner created"
fi
csrf=$(call POST /api/v1/auth/login "$creds" | json '["csrfToken"]')

path="smoke-$RANDOM$RANDOM"
workflow=$(cat <<EOF
{"name":"smoke $path","nodes":[
  {"name":"Hook","type":"engi.trigger.webhook","position":[0,0],
   "parameters":{"httpMethod":"POST","path":"$path","responseMode":"lastNode"}},
  {"name":"Greet","type":"engi.transform.set","position":[1,0],
   "parameters":{"fields":[{"name":"greeting","type":"string","value":"={{ 'hello ' + \$json.body.name }}"}],"keepOtherFields":false}}],
 "connections":[{"from":{"node":"Hook"},"to":{"node":"Greet"}}]}
EOF
)
id=$(call POST /api/v1/workflows "$workflow" | json '["id"]')
call POST "/api/v1/workflows/$id/activate" "{}" >/dev/null
echo "workflow $id active at /hook/$path"

reply=$(curl -fsS -X POST -H "Content-Type: application/json" --data '{"name":"engi"}' "$hooks/hook/$path")
echo "webhook replied: $reply"
greeting=$(json '["greeting"]' <<<"$reply")
[[ $greeting == "hello engi" ]] || { echo "unexpected reply" >&2; exit 1; }

execs=$(call GET "/api/v1/executions?workflowId=$id")
echo "executions stored: $(json '["data"].__len__()' <<<"$execs")"
call POST "/api/v1/workflows/$id/deactivate" "{}" >/dev/null
echo "smoke test passed"

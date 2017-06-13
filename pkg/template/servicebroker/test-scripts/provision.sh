#!/bin/bash -e

. shared.sh

serviceUUID=${serviceUUID-$(oc get template cakephp-mysql-example -n openshift -o template --template '{{.metadata.uid}}')}

req="{
  \"plan_id\": \"$planUUID\",
  \"service_id\": \"$serviceUUID\",
  \"context\": {
    \"platform\": \"kubernetes\",
    \"namespace\": \"$namespace\"
  },
  \"parameters\": {
    \"MYSQL_USER\": \"username\"
  }
}"

curl \
  -X PUT \
  -H 'X-Broker-API-Version: 2.9' \
  -H 'Content-Type: application/json' \
  -H "X-Broker-API-Originating-Identity: Kubernetes $(echo -ne "{\"Name\": \"$requesterUsername\"}" | base64)" \
  -d "$req" \
  -v \
  $curlargs \
  "$endpoint/v2/service_instances/$instanceUUID?accepts_incomplete=true"

# Kafka demo

A small end-to-end check of Engi in Kubernetes with Kafka: one order event goes
into the `orders` topic, a Kafka Trigger workflow picks it up and a Set node
computes the order total.

| File | What it is |
|---|---|
| `kafka.yaml` | Single-node Kafka (KRaft) in the `infra` namespace. Demo only; use your own Kafka otherwise. |
| `producer.yaml` | Mock producer Job: creates `orders` and sends one `{"orderId","customer","qty","price"}` event. |
| `workflow.json` | Kafka Trigger (`orders`) → Set node `Total` (`qty * price`). |
| `run.sh` | Creates the credential and workflow, activates it, runs the producer, waits for the execution. |

```bash
kubectl apply -f deploy/examples/kafka-demo/kafka.yaml
# Engi's NetworkPolicy must allow egress to 9092 (networkPolicy.egressPorts)
kubectl port-forward -n engi svc/engi-api 8081:8080 &
deploy/examples/kafka-demo/run.sh http://localhost:8081
# ...
# execution: ex_... success
# Total node output: {"customer": "ada", "orderId": "ord-...", "total": 37.5}
```

Each `run.sh` creates a new workflow; to send more events to the one you have,
rerun only the producer:

```bash
kubectl delete job order-producer -n infra --ignore-not-found
kubectl apply -f deploy/examples/kafka-demo/producer.yaml
```

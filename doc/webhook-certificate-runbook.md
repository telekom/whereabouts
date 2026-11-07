# Webhook Certificate Rotation and Revocation Runbook

The operator's validating webhook server uses a self-signed CA and a server
certificate managed by
[cert-controller](https://github.com/open-policy-agent/cert-controller). This
runbook covers forcing a re-issue (for example after a suspected key
compromise), what happens to requests in flight, and how to verify the result.

## Background

| Item | Value |
|------|-------|
| Secret | `--webhook-secret-name` (Helm: `<fullname>-webhook-cert`, kustomize: `whereabouts-webhook-cert`) in the operator namespace |
| Secret keys | `ca.crt`, `ca.key` (CA), `tls.crt`, `tls.key` (server) |
| CA validity | 10 years (cert-controller default) |
| Server certificate validity | 1 year (cert-controller default) |
| Automatic renewal | Checked every 12 hours, and on every change to the Secret; renewed when less than 90 days remain |
| CA bundle | Injected into `clientConfig.caBundle` of the ValidatingWebhookConfiguration (`--webhook-config-name`); the injected bundle replaces the old one, so a new CA stops the API server trusting the old CA |
| Serving | Every operator replica serves webhooks from the Secret mounted at `--cert-dir`; the server reloads changed files automatically |

The certificates are self-signed and there is no CRL or OCSP. **Revoking a
certificate means replacing it**: the API server only trusts the CA in
`caBundle`, so issuing a new CA and bundle makes the old key pair useless.

## Force a re-issue

Set the variables for your installation:

```bash
NS=<operator namespace>
SECRET=<webhook secret name>          # e.g. whereabouts-webhook-cert
VWC=<validating webhook config name>  # e.g. whereabouts-validating-webhook-configuration
DEPLOY=<operator deployment name>
```

### Server key pair only

Use this when the server key may be exposed but the CA key is not (the CA key
lives in the same Secret, so a Secret leak exposes both; then use the full
procedure below).

```bash
kubectl -n "$NS" patch secret "$SECRET" --type=merge \
  -p '{"data":{"tls.crt":"","tls.key":""}}'
```

### CA and server key pair (revocation)

```bash
kubectl -n "$NS" patch secret "$SECRET" --type=merge \
  -p '{"data":{"ca.crt":"","ca.key":"","tls.crt":"","tls.key":""}}'
```

Do not delete the Secret. The operator creates it only at startup; until then
the rotator cannot write new certificates.

### What the operator does

1. Every replica watches the Secret. The first replica to see the emptied keys
   generates new material and writes it to the Secret (the others retry on
   conflict and then find valid certificates).
2. That replica exits with status 0 (`RestartOnSecretRefresh`) and is
   restarted by the kubelet. It logs `Secrets have been updated; exiting so pod
   can be restarted`.
3. The new CA is injected into the ValidatingWebhookConfiguration.
4. The other replicas reload the certificate once the kubelet syncs the
   mounted Secret (usually within a minute or two).

To apply the new certificate on all replicas at once, restart them:

```bash
kubectl -n "$NS" rollout restart deployment/"$DEPLOY"
kubectl -n "$NS" rollout status deployment/"$DEPLOY"
```

## In-flight requests

- The replica that writes the new certificate exits immediately. Admission
  requests it is handling are dropped, and the API server applies the
  webhook's `failurePolicy`.
- Between the CA bundle update and a replica loading the new certificate,
  the API server rejects that replica's old certificate during the TLS
  handshake, and again the `failurePolicy` applies.
- With the default `failurePolicy: Ignore`, these requests are admitted
  without validation; the reconcilers repair any inconsistent state on their
  next run. With `Fail`, the affected requests are rejected and clients retry.
- A rolling restart stops pods with SIGTERM. The webhook server stops
  accepting new connections and completes requests in flight within the
  termination grace period. The PodDisruptionBudget (more than one replica)
  keeps at least one replica serving.

To keep the window short, run the re-issue during low CRD write activity and
follow it with the rolling restart above.

## Verify

1. The Secret holds a new certificate (check serial and dates):

   ```bash
   kubectl -n "$NS" get secret "$SECRET" -o jsonpath='{.data.tls\.crt}' \
     | base64 -d | openssl x509 -noout -serial -subject -issuer -dates
   ```

2. The CA bundle matches the CA in the Secret:

   ```bash
   kubectl -n "$NS" get secret "$SECRET" -o jsonpath='{.data.ca\.crt}' \
     | base64 -d | openssl x509 -noout -fingerprint -sha256
   kubectl get validatingwebhookconfiguration "$VWC" \
     -o jsonpath='{.webhooks[0].clientConfig.caBundle}' \
     | base64 -d | openssl x509 -noout -fingerprint -sha256
   ```

3. Each replica serves the new certificate (repeat for every pod):

   ```bash
   kubectl -n "$NS" port-forward pod/<operator-pod> 9443:9443 &
   openssl s_client -connect 127.0.0.1:9443 </dev/null 2>/dev/null \
     | openssl x509 -noout -serial
   ```

   The serial must match step 1.

4. The pinned TLS policy is in force (unless `--webhook-tls-go-defaults` is
   set). Both of these handshakes must fail:

   ```bash
   openssl s_client -connect 127.0.0.1:9443 -tls1_1 </dev/null
   openssl s_client -connect 127.0.0.1:9443 -tls1_2 -cipher AES256-SHA </dev/null
   ```

5. All operator pods are ready, and the API server reaches the webhook: a
   dry-run of an invalid IPPool is rejected.

   ```bash
   kubectl -n "$NS" get pods -l app=<fullname>-operator
   kubectl apply --dry-run=server -f - <<'EOF'
   apiVersion: whereabouts.cni.cncf.io/v1alpha1
   kind: IPPool
   metadata:
     name: runbook-check
     namespace: kube-system
   spec:
     range: not-a-cidr
     allocations: {}
   EOF
   ```

   The request must fail with a webhook validation error. If it is admitted,
   the API server is not reaching the webhook (with `failurePolicy: Ignore`)
   and steps 2 and 3 need another look.

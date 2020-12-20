# GoDaddy webhook for cert-manager

This is a webhook solver for [GoDaddy](https://godaddy.com).

[![Docker Repository on Quay](https://quay.io/repository/neogeneva/cert-manager-webhook-godaddy/status "Docker Repository on Quay")](https://quay.io/repository/neogeneva/cert-manager-webhook-godaddy)

## Requirements

* [cert-manager](https://github.com/jetstack/cert-manager): *tested with 0.12.0*
* [helm](https://helm.sh/): *tested with 3.0.0* 

## Installing

```bash
helm repo add godaddy-webhook https://raw.githubusercontent.com/neoGeneva/cert-manager-webhook-godaddy/master/deploy/repo
helm repo update
helm install --namespace cert-manager cert-manager-webhook-godaddy godaddy-webhook/cert-manager-webhook-godaddy
```

## Configuration

1. Generate API Key and Token from GoDaddy https://developer.godaddy.com/
2. Create secret to store the API Token
```bash
kubectl --namespace cert-manager create secret generic \
    godaddy-api-key --from-literal=key='<GODADDY_AUTH_KEY>'
```

3. Configure your ClusterIssuer to reference the GoDaddy webhook.
```yaml
apiVersion: cert-manager.io/v1alpha2
kind: ClusterIssuer
metadata:
  name: ...
spec:
  acme:
    solvers:
    - dns01:
        webhook:
          groupName: acme.blackhouse.dev
          solverName: godaddy
          config:
            authAPIKey: <GODADDY_AUTH_TOKEN>
            authAPISecretRef:
              name: godaddy-api-key
              key: key
```

## Development

All DNS providers **must** run the DNS01 provider conformance testing suite,
else they will have undetermined behaviour when used with cert-manager.

**It is essential that you configure and run the test suite when creating a
DNS01 webhook.**

An example Go test file has been provided in [main_test.go]().

Before you can run the test suite, you need to download the test binaries:

```bash
./scripts/fetch-test-binaries.sh
```

Then create `testdata/my-custom-solver/config.json` and `testdata/my-custom-solver/api-key.yaml`
to setup the configs, using the `*.sample` files a reference

Now you can run the test suite with:

```bash
TEST_ZONE_NAME=example.com. go test .
```
# Development

## Tests

Run the tests with `go test -race ./...`.
They need no cluster, no Docker and no root.
They include `TestScenariosHermetic`, which runs the end-to-end scenarios against a fake Kubernetes API server.
The fake server lives in `internal/kube/kubetest`.
Each scenario gets its own fresh server, so the scenarios are independent and finish in a fraction of a second.

## Scenarios on a kind cluster

The same scenarios can also run against a real, disposable [kind](https://kind.sigs.k8s.io/) cluster.
Use this run before a release, because it needs Docker and takes a few minutes.
It refuses to run on a context whose name does not start with `kind-`.
Each scenario creates its own namespace with a random name, deletes it at the end,
and uses a temporary proxy port instead of port 80.

```bash
kind create cluster --name kst-e2e --kubeconfig /tmp/kst-e2e.kubeconfig
KST_E2E_KUBECONFIG=/tmp/kst-e2e.kubeconfig KST_E2E_CONTEXT=kind-kst-e2e \
  go test -tags e2e -race -count=1 -run TestScenariosKind ./cmd/dns
kind delete cluster --name kst-e2e
```

## Continuous integration

GitHub Actions runs the checks in `.github/workflows/ci.yml` on every push to `main` and on every pull request.
The checks are gofmt, `go vet`, golangci-lint, govulncheck and the tests with the race detector on Linux and macOS.
The test job also fails when the total coverage drops below 95%.
A release tag runs the same checks and the kind scenarios, and GoReleaser builds the release only after they pass.

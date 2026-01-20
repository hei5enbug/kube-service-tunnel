# kube-service-tunnel

kube-service-tunnel is a terminal UI for developers who want to open Kubernetes services from their own machine.
It port-forwards a pod of each service you pick and adds its hostname to `/etc/hosts`.
You then open the service at `http://<service>.<namespace>` in your browser.

**Contents:** [Features](#features) · [Requirements](#requirements) · [Installation](#installation) ·
[First Use](#first-use) · [Documentation](#documentation) · [Development](#development) ·
[Help and Releases](#help-and-releases)

## Features

- Interactive terminal UI for managing Kubernetes services
- Automatic port forwarding for services
- DNS management via `/etc/hosts`
- Every tunnel is reached at `http://<hostname>` on port 80, where the default hostname is `<service>.<namespace>`
- Custom hostnames and local ports for each tunnel
- Support for multiple Kubernetes contexts
- System namespace filtering (kube-system, kube-public, kube-node-lease)
- One key (Ctrl+P) registers every service of a context
- A tunnel is removed automatically when its pod goes away

## Requirements

- macOS or Linux, on amd64 or arm64. Each release has a binary for these four combinations.
- Access to a Kubernetes cluster through a kubeconfig file
- Sudo privileges, because the tool modifies `/etc/hosts`
- A free port 80 on `127.0.0.1`, because the tool's proxy listens there

**Note:** Go is only required when building from source. Homebrew installation does not require Go.

## Installation

### Homebrew (macOS)

```bash
brew tap hei5enbug/kube-service-tunnel
brew install --cask kube-service-tunnel
```

### Manual Installation

Download the archive for your system from [GitHub Releases](https://github.com/hei5enbug/kube-service-tunnel/releases).
Extract the `kube-service-tunnel` binary and move it to a directory in your PATH.

## First Use

Start the tool with sudo:

```bash
sudo kube-service-tunnel
```

1. The Context pane has the focus. Move to a context with the arrow keys and press Enter.
2. In the Namespace pane, press Enter on a namespace.
3. In the Services pane, press Enter on a service that has no `✗` mark.
4. Open `http://<service>.<namespace>` in your browser.

Press `?` to see the keys, and press Ctrl+C to quit.
When you exit, the tool removes its section of `/etc/hosts` and stops all port forwards.

### Command Line Options

- `--kubeconfig`: Path to kubeconfig file (default: ~/.kube/config)

Under `sudo`, the default kubeconfig is the `~/.kube/config` of the user who ran `sudo` (read from `SUDO_USER`).
The `--kubeconfig` option overrides this default.

## Documentation

| Document | Question it answers |
|---|---|
| [How It Works](docs/how-it-works.md) | What do the panes, marks, dialogs and status line show, and how does each action behave? |
| [Eligible Services](docs/how-it-works.md#eligible-services) | Why is a service marked `✗`, and which services can I register? |
| [Editing a Tunnel](docs/how-it-works.md#editing-a-tunnel) | How do I change the hostname or the local port of a tunnel? |
| [Lost Tunnels](docs/how-it-works.md#lost-tunnels) | What happens when the pod behind a tunnel goes away? |
| [Key Bindings](docs/key-bindings.md) | Which key does what in each pane and dialog? |
| [Development](docs/development.md) | How do I run the tests, the kind scenarios and the CI checks? |

## Development

Run the tests with `go test -race ./...`.
They need no cluster, no Docker and no root.
[Development](docs/development.md) explains the end-to-end scenarios, the kind cluster run and the CI checks.

## Help and Releases

- Report a bug or ask a question in [GitHub Issues](https://github.com/hei5enbug/kube-service-tunnel/issues).
- [GitHub Releases](https://github.com/hei5enbug/kube-service-tunnel/releases) has the binaries and the changes in
  each release.

# kube-service-tunnel

Interactive TUI for Kubernetes service port forwarding and DNS management.

## Features

- Interactive terminal UI for managing Kubernetes services
- Automatic port forwarding for services
- DNS management via `/etc/hosts`
- Every service is reached at `http://<service>.<namespace>` on port 80
- Support for multiple Kubernetes contexts
- System namespace filtering (kube-system, kube-public, kube-node-lease)

## Installation

### Homebrew (macOS)

```bash
brew tap hei5enbug/kube-service-tunnel
brew install --cask kube-service-tunnel
```

### Manual Installation

Download the latest release from [GitHub Releases](https://github.com/hei5enbug/kube-service-tunnel/releases) and extract the binary to your PATH.


## Usage

```bash
sudo kube-service-tunnel
```

**Note:** This application requires sudo privileges to modify `/etc/hosts`.

### Command Line Options

- `--kubeconfig`: Path to kubeconfig file (default: ~/.kube/config)

Under `sudo`, the default kubeconfig is the `~/.kube/config` of the user who ran `sudo` (read from `SUDO_USER`).
The `--kubeconfig` option overrides this default.

## How It Works

### Access URL

After you register a service, open `http://<service>.<namespace>` in your browser.
The URL always uses port 80, whatever port the service itself uses.
Each service name and namespace pair can be registered only once.
This holds even when the same pair exists in another context.
The message window tells you which context already owns the name.

### Eligible Services

A service can be registered only if it is a ClusterIP service with a pod selector.
It also needs a TCP port that is 80, 8080, 3000, 8000 or 9000.
NodePort, LoadBalancer and headless services are not listed.
Services without a pod selector or a supported port are still listed.
Registering one of them fails, and the message window says why.

### Lost Tunnels

A port forward can drop, for example when a pod restarts.
The tool then removes that tunnel from `/etc/hosts`, the proxy and the tunnel list.
The message window shows which tunnel was lost.
Press Enter on the service to register it again.

### Running and Exiting

Only one instance of the tool can run at a time.
A second instance prints an error and exits.
When you exit, the tool removes its section of `/etc/hosts` and stops all port forwards.
Ctrl+C works even while the tool is loading.

## Key Bindings

- **Tab**: Navigate to next window
- **Shift+Tab**: Navigate to previous window
- **Enter**: Select context/namespace/service or register port forward
- **Ctrl+P**: Register all services in selected context (Context window)
- **Delete**: Delete port forward (Local DNS Tunnels window)
- **Ctrl+B**: Change background color
- **Ctrl+T**: Change text color
- **Ctrl+C**: Exit application

## Requirements

- Kubernetes cluster access
- Sudo privileges (for `/etc/hosts` modification)

**Note:** Go is only required when building from source. Homebrew installation does not require Go.

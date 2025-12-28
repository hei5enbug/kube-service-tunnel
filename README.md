# kube-service-tunnel

Interactive TUI for Kubernetes service port forwarding and DNS management.

## Features

- Interactive terminal UI for managing Kubernetes services
- Automatic port forwarding for services
- DNS management via `/etc/hosts`
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

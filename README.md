# kube-service-tunnel

Interactive TUI for Kubernetes service port forwarding and DNS management.

## Features

- Interactive terminal UI for managing Kubernetes services
- Automatic port forwarding for services
- DNS management via `/etc/hosts`
- Every tunnel is reached at `http://<hostname>` on port 80, where the default hostname is `<service>.<namespace>`
- Custom hostnames and local ports for each tunnel
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

### Screen Layout

The top row shows the tool name, the active context and the number of tunnels.
The left column stacks the **Context** pane over the **Namespace** pane.
The **Services** pane and the **Tunnels** pane fill the rest of the width.
The Tunnels pane adds a POD column when it is at least 55 columns wide.
The two bottom rows hold the status line and a line of key hints.
The key hints change with the focused pane and with any open dialog.

Text that does not fit ends with `…`.
Press `i` to see the full value.

A terminal narrower than 80 columns or lower than 16 rows uses a compact layout.
The compact layout shows only the focused pane, and the top row lists the panes as tabs.
Tab and the keys `1` to `4` switch the shown pane.
Resizing keeps the focused pane, the filters and the cursor position.

### Markers

| Marker | Pane | Meaning |
|---|---|---|
| `▶` | Context, Namespace | The active context or namespace. |
| `!` | Context | Discovery failed for this context. |
| `●` | Services | The service has a tunnel. The NOTE column shows `→ <hostname>` when the hostname is not the default. |
| `○` | Services | Another tunnel uses the hostname this service would get. The NOTE column names that tunnel's context. |
| `✗` | Services | The service cannot be registered. The NOTE column gives the reason. |

The Services pane checks these cases in the order of the table and shows the first one that applies.
For example, a service shows `○` instead of `✗` when another tunnel already uses its hostname.
The cursor highlight is separate from `▶`.
Moving the cursor does not change the active context or namespace.
Press Enter to make the row under the cursor active.

### Access URL

After you register a service, open `http://<hostname>` in your browser.
The default hostname is `<service>.<namespace>`, and you can change it with `e` in the Tunnels pane.
The URL always uses port 80, whatever port the service itself uses.
A proxy on port 80 sends each hostname to the local port of its tunnel.
The local port is a port forward on `127.0.0.1`, so `127.0.0.1:<local port>` also reaches the service directly.

Each service of a context can have one tunnel.
Each hostname can belong to only one tunnel, even across contexts.
Enter on a service with `●` is refused, and the status line shows the URL of its tunnel.
Enter on a service with `○` is refused, and the status line names the context that uses the hostname.
To free that hostname, rename the other tunnel with `e` in the Tunnels pane first.

### Eligible Services

A service can be registered only if it is a ClusterIP service with a pod selector.
It also needs a TCP port that is 80, 8080, 3000, 8000 or 9000.
NodePort, LoadBalancer and headless services are not listed.
Services without a pod selector or a supported port are still listed with `✗`.
The PORT column shows the service port that registration would use.
The PORT column shows `-` when the service cannot be registered.

### Registering All Services

Ctrl+P in the Context pane registers every service of the context under the cursor.
It covers the whole context, even when a filter hides some rows.
A dialog first shows how many services and namespaces it covers.
The dialog also shows how many services it skips because they cannot be registered or are already registered.
A service counts as already registered when it shows `●` or `○`.
Each service uses the hostname and local port remembered for it.
The Cancel button has the focus when the dialog opens.
Press `y` to register, or press `n` or Esc to cancel.
The dialog uses the list from the last refresh, and the tool checks each service again while it registers.
The loading dialog shows which service is being registered.
The status line then sums up the result.
The history lists every service that was excluded before the run, skipped, could not be registered or failed.
It also lists every service whose remembered local port was busy, together with the port used instead.
A run with a busy port ends with a warning.

### Editing a Tunnel

Press `e` in the Tunnels pane to change the hostname and the local port of the tunnel under the cursor.
The dialog shows the service and two fields, **Hostname** and **Local port**.
The Hostname field is filled in only when the tunnel uses a custom hostname.
The Local port field is filled in only when you chose a port for the tunnel.
An empty field uses the default: the hostname `<service>.<namespace>` or an automatic local port.
Clearing a field and saving resets that setting to the default.

A hostname may contain letters, digits, hyphens and dots, and the tool stores it in lower case.
Each part between dots must start and end with a letter or a digit.
An IP address is not a valid hostname.
A hostname also cannot end with a number, such as `127.1` or `app.123`, because browsers read it as an IP address.
A local port must be a number from 1 to 65535.
Port 80 is not allowed because the proxy uses it.
An invalid field shows an error line in the dialog, and the dialog stays open until you fix it or cancel.

The local port must also be free on `127.0.0.1`.
If it is busy, the update fails and the status line shows the error.
The old tunnel keeps working until the new setting is in place, so a failed update leaves the tunnel as it was.
When the update succeeds, the tool adds the new hostname to `/etc/hosts` and removes the old one.
If only removing the old hostname or the old port forward fails, the update still applies with a warning.
The cursor stays on the edited tunnel, even when its new hostname moves it in the list.

The tool remembers the hostname and local port of each service while it runs.
Enter in the Services pane and Ctrl+P reuse them, also after a tunnel was lost or removed.
The settings are never saved to disk, so every start begins with the defaults.
When a remembered port is busy at registration, the tool uses an automatic port and shows a warning.

### Filter and Details

Press `/` to filter the focused pane.
The filter keeps rows whose name contains the typed text, ignoring upper and lower case.
In the Tunnels pane the filter matches the URL or the context.
The pane title shows the filter and the number of matching rows.
Enter keeps the filter, and Esc clears it.
Choosing another context clears the Namespace and Services filters.
Choosing another namespace clears the Services filter.

Press `i` to open details for the row under the cursor.
Context details include the full discovery error.
Service details include every port, the selector and the reason a service cannot be registered.
Tunnel details include the service, the hostname, the local port, the pod and the pod port.
The hostname is marked `(default)` or `(custom)`.
The local port is marked `(auto)` or `(set)`, or names the chosen port when that port was busy.

### Status Line and History

The status line shows the latest event with an icon.
Warnings start with `⚠ WARN`, and errors start with `✗ ERROR`.
Information and success messages disappear after 5 seconds.
Warnings and errors stay until a newer event replaces them or you press Esc in a pane.
A long or multi-line message ends with `(F2)`.

Press F2 to open the history of the last 500 events.
The top row shows `⚠ N unread · F2` when warnings or errors arrived since you last opened the history.
Opening the history resets that count.

### Refresh

Press `r` to read the services of the active context again.
Other contexts keep the list they last loaded.
Refreshing keeps the tunnels, the filters, the selected namespace and the cursor when they still exist.

### Lost Tunnels

A tunnel is lost when its pod is deleted, starts terminating, is replaced by a new pod with the same name, or stops.
A tunnel is also lost when the port forward itself ends, for example when the connection to the cluster breaks.
The tool watches the forwarded pod, so it notices a lost tunnel within seconds.
The tool then removes that tunnel from `/etc/hosts`, the proxy and the tunnel list.
The status line shows a warning with the lost URL and its context.
Press Enter on the service to register it again.
The tool reuses the hostname and local port you set for that service.

### Copying a URL

Press `c` in the Tunnels pane to copy the full URL of the tunnel under the cursor.
On macOS the tool uses `pbcopy` in the session of the user who ran `sudo`.
If that fails, the tool asks the terminal to copy the URL with the OSC 52 escape sequence.
On other systems the tool always uses OSC 52.
Some terminals ignore that request, so press `i` to read the URL when nothing was copied.

### Themes

Press F4 to switch between the Dark, Light and Mono themes.
Mono uses the terminal's own colors and marks the cursor row with reverse video.
Every start uses the Dark theme.

### Running and Exiting

Only one instance of the tool can run at a time.
A second instance prints an error and exits.
When you exit, the tool removes its section of `/etc/hosts` and stops all port forwards.
Ctrl+C works even while the tool is loading.

## Key Bindings

| Where | Key | Action |
|---|---|---|
| Everywhere | Ctrl+C | Quit, also while loading or in a dialog |
| Any pane | Tab / Shift+Tab | Move to the next or previous pane |
| Any pane | `1` to `4` | Jump to a pane |
| Any pane | Arrows, `j`, `k`, `g`, `G`, PgUp, PgDn | Move the cursor |
| Any pane | `/` | Filter the pane |
| Any pane | `i` | Show details of the row under the cursor |
| Any pane | `r` | Refresh the active context |
| Any pane | `?` or F1 | Show help |
| Any pane | F2 | Show the history |
| Any pane | F4 | Switch to the next theme |
| Any pane | Esc | Clear the filter, or clear the status line when there is no filter |
| Context | Enter | Make the context active and move to the Namespace pane |
| Context | Ctrl+P | Register all services of the context under the cursor |
| Namespace | Enter | Make the namespace active and move to the Services pane |
| Services | Enter | Register the service under the cursor |
| Tunnels | `e` | Edit the hostname and local port of the tunnel under the cursor |
| Tunnels | `d`, Delete or Backspace | Remove the tunnel under the cursor |
| Tunnels | `c` | Copy the URL of the tunnel under the cursor |
| Filter input | Enter | Keep the filter and return to the pane |
| Filter input | Esc | Clear the filter and return to the pane |
| Filter input | Tab / Shift+Tab | Keep the filter and move to the next or previous pane |
| Filter input | F1 | Show help |
| Register dialog | `y` / `n` | Register or cancel |
| Register dialog | Left / Right / Tab, Enter | Choose a button and press it |
| Register dialog | Up / Down, PgUp / PgDn | Scroll the dialog text |
| Edit dialog | Tab / Shift+Tab | Move between the fields and the buttons |
| Edit dialog | Enter | Move from Hostname to Local port, save from Local port or Save, or cancel on Cancel |
| Dialog | Esc | Close or cancel the dialog |

Letter and number keys work only when a pane has the focus and no dialog is open.
While you type a filter or fill in the edit dialog, they become part of the text.

## Requirements

- Kubernetes cluster access
- Sudo privileges (for `/etc/hosts` modification)

**Note:** Go is only required when building from source. Homebrew installation does not require Go.

## Development

Run the unit tests with `go test -race ./...`.
They need no cluster and no root.

The end-to-end test runs the real port forward and proxy against a disposable [kind](https://kind.sigs.k8s.io/) cluster.
It refuses to run on a context whose name does not start with `kind-`.
It creates the namespace `kst-e2e`, deletes it at the end, and uses a temporary proxy port instead of port 80.

```bash
kind create cluster --name kst-e2e --kubeconfig /tmp/kst-e2e.kubeconfig
KST_E2E_KUBECONFIG=/tmp/kst-e2e.kubeconfig KST_E2E_CONTEXT=kind-kst-e2e \
  go test -tags e2e -race -count=1 -run TestE2EAgainstKind ./cmd/dns
kind delete cluster --name kst-e2e
```

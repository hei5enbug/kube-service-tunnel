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
| `●` | Services | The service is registered. The NOTE column names the owner when another context owns it. |
| `✗` | Services | The service cannot be registered. The NOTE column gives the reason. |

A service shows `●` instead of `✗` when another context already registered its name.
The cursor highlight is separate from `▶`.
Moving the cursor does not change the active context or namespace.
Press Enter to make the row under the cursor active.

### Access URL

After you register a service, open `http://<service>.<namespace>` in your browser.
The URL always uses port 80, whatever port the service itself uses.
Each service name and namespace pair can be registered only once.
This holds even when the same pair exists in another context.
The status line tells you which context already owns the name.

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
The Cancel button has the focus when the dialog opens.
Press `y` to register, or press `n` or Esc to cancel.
The dialog uses the list from the last refresh, and the tool checks each service again while it registers.
The loading dialog shows which service is being registered.
The status line then sums up the result.
The history lists every service that was excluded before the run, skipped, could not be registered or failed.

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
Tunnel details include the pod and the local address of the port forward.

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

A port forward can drop, for example when a pod restarts.
The tool then removes that tunnel from `/etc/hosts`, the proxy and the tunnel list.
The status line shows a warning with the lost URL and its context.
Press Enter on the service to register it again.

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
| Tunnels | `d`, Delete or Backspace | Remove the tunnel under the cursor |
| Tunnels | `c` | Copy the URL of the tunnel under the cursor |
| Filter input | Enter | Keep the filter and return to the pane |
| Filter input | Esc | Clear the filter and return to the pane |
| Filter input | Tab / Shift+Tab | Keep the filter and move to the next or previous pane |
| Filter input | F1 | Show help |
| Register dialog | `y` / `n` | Register or cancel |
| Register dialog | Left / Right / Tab, Enter | Choose a button and press it |
| Register dialog | Up / Down, PgUp / PgDn | Scroll the dialog text |
| Dialog | Esc | Close or cancel the dialog |

Letter and number keys work only when a pane has the focus and no dialog is open.
While you type a filter, they become part of the filter text.

## Requirements

- Kubernetes cluster access
- Sudo privileges (for `/etc/hosts` modification)

**Note:** Go is only required when building from source. Homebrew installation does not require Go.

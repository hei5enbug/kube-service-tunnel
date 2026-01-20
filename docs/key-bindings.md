# Key Bindings

This table lists every key of the tool.
[How It Works](how-it-works.md) explains what each action does.

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

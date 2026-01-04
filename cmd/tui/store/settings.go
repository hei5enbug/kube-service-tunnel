package store

import (
	"maps"
	"strings"
)

// settingKeySeparator cannot appear in a context, namespace or service name, so every key stays unique.
const settingKeySeparator = "\x00"

// TunnelSetting is the hostname and local port remembered for one service while the app runs. An empty
// Hostname means the default hostname and a zero LocalPort means an automatic port.
type TunnelSetting struct {
	Hostname  string
	LocalPort int32
}

// SettingKey identifies a service in TunnelSettings by its context, namespace and name.
func SettingKey(contextName, namespace, service string) string {
	return strings.Join([]string{contextName, namespace, service}, settingKeySeparator)
}

// SetTunnelSetting remembers setting for key in a new map. A zero setting forgets the key.
func (store *Store) SetTunnelSetting(key string, setting TunnelSetting) {
	store.setState(func(state *State) bool {
		if state.TunnelSettings[key] == setting {
			return false
		}
		next := make(map[string]TunnelSetting, len(state.TunnelSettings)+1)
		maps.Copy(next, state.TunnelSettings)
		if setting == (TunnelSetting{}) {
			delete(next, key)
		} else {
			next[key] = setting
		}
		state.TunnelSettings = next
		return true
	})
}

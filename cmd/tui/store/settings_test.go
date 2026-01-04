package store

import (
	"reflect"
	"testing"
)

func TestSetTunnelSetting(t *testing.T) {
	web := SettingKey("dev", "api", "web")
	admin := SettingKey("dev", "api", "admin")

	t.Run("설정은 새 맵에 기억하고 이전 상태의 맵은 바꾸지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetTunnelSetting(web, TunnelSetting{Hostname: "api.local"})
		before := s.GetState().TunnelSettings

		// when
		s.SetTunnelSetting(admin, TunnelSetting{LocalPort: 8080})

		// then
		if len(before) != 1 || before[web] != (TunnelSetting{Hostname: "api.local"}) {
			t.Fatalf("previous map = %v", before)
		}
		after := s.GetState().TunnelSettings
		if len(after) != 2 || after[admin] != (TunnelSetting{LocalPort: 8080}) || after[web] != before[web] {
			t.Fatalf("settings = %v", after)
		}
	})

	t.Run("빈 설정은 키를 지우고 이전 상태의 맵은 그대로 둔다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetTunnelSetting(web, TunnelSetting{Hostname: "api.local", LocalPort: 8080})
		before := s.GetState().TunnelSettings

		// when
		s.SetTunnelSetting(web, TunnelSetting{})

		// then
		if _, found := s.GetState().TunnelSettings[web]; found {
			t.Fatalf("settings = %v, want the key deleted", s.GetState().TunnelSettings)
		}
		if before[web] != (TunnelSetting{Hostname: "api.local", LocalPort: 8080}) {
			t.Fatalf("previous map = %v", before)
		}
	})

	t.Run("같은 설정을 다시 넣으면 상태를 바꾸지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetTunnelSetting(web, TunnelSetting{LocalPort: 8080})
		before := s.GetState().TunnelSettings

		// when
		s.SetTunnelSetting(web, TunnelSetting{LocalPort: 8080})

		// then
		if reflect.ValueOf(s.GetState().TunnelSettings).UnsafePointer() != reflect.ValueOf(before).UnsafePointer() {
			t.Fatal("the settings map was replaced although nothing changed")
		}
	})

	t.Run("없는 키를 빈 설정으로 지우면 상태를 바꾸지 않는다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.SetTunnelSetting(web, TunnelSetting{})

		// then
		if settings := s.GetState().TunnelSettings; settings != nil {
			t.Fatalf("settings = %v, want nil", settings)
		}
	})

	t.Run("SettingKey는 컨텍스트 이름에 /가 있어도 다른 서비스와 키가 겹치지 않는다", func(t *testing.T) {
		// given
		first, second := []string{"cluster/dev", "api", "web"}, []string{"cluster", "dev/api", "web"}

		// when
		keys := []string{SettingKey(first[0], first[1], first[2]), SettingKey(second[0], second[1], second[2])}

		// then
		if keys[0] == keys[1] {
			t.Fatalf("keys collide: %q", keys[0])
		}
	})
}

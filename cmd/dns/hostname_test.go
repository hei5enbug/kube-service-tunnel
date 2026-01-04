package dns

import (
	"context"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "올바른 호스트 이름은 그대로 돌려준다", input: "api.local", want: "api.local"},
		{name: "대문자와 앞뒤 공백은 소문자로 바꾸고 다듬는다", input: "  API.Local ", want: "api.local"},
		{name: "끝의 점 하나는 제거한다", input: "api.local.", want: "api.local"},
		{name: "숫자와 하이픈이 든 레이블을 허용한다", input: "web-1.svc2", want: "web-1.svc2"},
		{name: "빈 값은 거절한다", input: "  ", wantErr: "hostname must not be empty"},
		{name: "점만 있으면 빈 값으로 거절한다", input: ".", wantErr: "hostname must not be empty"},
		{name: "IPv4 주소는 거절한다", input: "10.0.0.1", wantErr: "hostname must not be an IP address"},
		{name: "IPv6 주소는 거절한다", input: "::1", wantErr: "hostname must not be an IP address"},
		{name: "줄인 IPv4 표기는 브라우저가 IP로 읽으므로 거절한다", input: "127.1", wantErr: "must not end with a number"},
		{name: "숫자만 있는 이름은 거절한다", input: "1234", wantErr: "must not end with a number"},
		{name: "마지막 레이블이 숫자이면 거절한다", input: "app.123", wantErr: "must not end with a number"},
		{name: "마지막 레이블이 16진수 표기이면 거절한다", input: "web.0x1f", wantErr: "must not end with a number"},
		{name: "숫자로 시작해도 마지막 레이블이 숫자가 아니면 허용한다", input: "123.web", want: "123.web"},
		{name: "0x로 시작해도 16진수가 아니면 허용한다", input: "web.0xyz", want: "web.0xyz"},
		{name: "빈 레이블은 거절한다", input: "a..b", wantErr: `hostname label "" is invalid: use letters, digits and hyphens`},
		{name: "허용하지 않는 문자는 거절한다", input: "a_b.local", wantErr: `hostname label "a_b" is invalid`},
		{name: "하이픈으로 시작하는 레이블은 거절한다", input: "-a.local", wantErr: `hostname label "-a" is invalid`},
		{name: "하이픈으로 끝나는 레이블은 거절한다", input: "a-.local", wantErr: `hostname label "a-" is invalid`},
		{name: "63자를 넘는 레이블은 거절한다", input: strings.Repeat("a", 64) + ".local", wantErr: "is invalid"},
		{name: "전체 길이가 253자를 넘으면 거절한다", input: strings.Repeat(strings.Repeat("a", 50)+".", 5) + "com", wantErr: "longer than 253"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			input := tt.input

			// when
			got, err := NormalizeHostname(input)

			// then
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, err %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestRegisterDNSTunnelCustomSettings(t *testing.T) {
	t.Run("사용자 지정 호스트 이름과 포트로 등록하고 활성화된 터널을 돌려준다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		target := kube.ServiceRef{Namespace: "default", Name: "web", Hostname: "  Web.Local. ", LocalPort: 41000}

		// when
		tunnel, err := manager.RegisterDNSTunnel(context.Background(), "ctx", target)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tunnel.DNSURL != "web.local" || tunnel.LocalPort != 41000 || tunnel.RequestedLocalPort != 41000 || tunnel.Service != "web" {
			t.Fatalf("tunnel = %+v", tunnel)
		}
		if got := kubeFake.registerSeen[0]; got.Hostname != "web.local" || got.LocalPort != 41000 {
			t.Fatalf("kube target = %+v, want normalized hostname and port", got)
		}
		if !hostsFake.has("web.local") || proxyFake.routePort("web.local") != 41000 {
			t.Fatalf("hosts/proxy not set up for web.local")
		}
	})

	t.Run("잘못된 호스트 이름이나 포트는 쿠버네티스를 호출하지 않고 거절한다", func(t *testing.T) {
		tests := []struct {
			name   string
			target kube.ServiceRef
			want   string
		}{
			{"IP 호스트 이름", kube.ServiceRef{Namespace: "ns", Name: "a", Hostname: "127.0.0.1"}, "must not be an IP address"},
			{"범위를 벗어난 포트", kube.ServiceRef{Namespace: "ns", Name: "a", LocalPort: 70000}, "local port 70000 is out of range 1-65535"},
			{"음수 포트", kube.ServiceRef{Namespace: "ns", Name: "a", LocalPort: -1}, "out of range"},
			{"프록시 포트", kube.ServiceRef{Namespace: "ns", Name: "a", LocalPort: 80}, "local port 80 is used by the proxy"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// given
				manager, kubeFake, _, _ := newTestManager()

				// when
				_, err := manager.RegisterDNSTunnel(context.Background(), "ctx", tt.target)

				// then
				if err == nil || !strings.Contains(err.Error(), tt.want) || kubeFake.registerCalls != 0 {
					t.Fatalf("err = %v, kube calls = %d, want %q without kube call", err, kubeFake.registerCalls, tt.want)
				}
			})
		}
	})

	t.Run("같은 서비스는 다른 호스트 이름으로도 두 번 등록할 수 없다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
			t.Fatalf("first registration failed: %v", err)
		}

		// when
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web", Hostname: "other.local"})

		// then
		if err == nil || !strings.Contains(err.Error(), "default/web is already registered as http://web.default") {
			t.Fatalf("err = %v, want identity duplicate", err)
		}
		if kubeFake.registerCalls != 1 {
			t.Fatalf("kube register calls = %d, want 1", kubeFake.registerCalls)
		}
	})

	t.Run("다른 서비스가 쓰는 사용자 지정 호스트 이름은 거절한다", func(t *testing.T) {
		// given
		manager, _, _, _ := newTestManager()
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx-a", kube.ServiceRef{Namespace: "default", Name: "web", Hostname: "shared.local"}); err != nil {
			t.Fatalf("first registration failed: %v", err)
		}

		// when
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx-b", kube.ServiceRef{Namespace: "default", Name: "api", Hostname: "Shared.Local"})

		// then
		if err == nil || !strings.Contains(err.Error(), "shared.local is already registered (context ctx-a)") {
			t.Fatalf("err = %v, want owned hostname error", err)
		}
	})
}

func TestRegisterServicesCustomSettings(t *testing.T) {
	t.Run("잘못된 대상은 실패로, 이미 등록된 서비스는 건너뜀으로 분류하고 나머지만 넘긴다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "ns", Name: "dup", Hostname: "dup.local"}); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		kubeFake.bulkResult = kube.RegisterAllResult{
			Tunnels:  []kube.ServiceTunnel{tunnelForTarget("ctx", kube.ServiceRef{Namespace: "ns", Name: "ok", Hostname: "ok.local"}, 41001)},
			Failures: []kube.ServiceRegistrationFailure{{Namespace: "ns", Service: "late", Reason: "no pod"}},
		}
		targets := []kube.ServiceRef{
			{Namespace: "ns", Name: "bad", Hostname: "bad_host"},
			{Namespace: "ns", Name: "port", LocalPort: 80},
			{Namespace: "ns", Name: "dup"},
			{Namespace: "ns", Name: "ok", Hostname: "OK.local.", LocalPort: 41001},
		}

		// when
		result, err := manager.RegisterServices(context.Background(), "ctx", targets, nil)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(kubeFake.bulkTargets) != 1 || kubeFake.bulkTargets[0].Hostname != "ok.local" || kubeFake.bulkTargets[0].LocalPort != 41001 {
			t.Fatalf("kube targets = %+v, want only normalized ok", kubeFake.bulkTargets)
		}
		if len(result.Failures) != 3 || result.Failures[0].Service != "bad" || !strings.Contains(result.Failures[0].Reason, "bad_host") ||
			result.Failures[1].Service != "port" || !strings.Contains(result.Failures[1].Reason, "used by the proxy") ||
			result.Failures[2].Service != "late" {
			t.Fatalf("failures = %+v", result.Failures)
		}
		if len(result.Skipped) != 1 || result.Skipped[0].Service != "dup" || result.Skipped[0].Reason != "already registered as http://dup.local" {
			t.Fatalf("skipped = %+v", result.Skipped)
		}
		if len(result.Registered) != 1 || result.Registered[0].DNSURL != "ok.local" || result.Registered[0].Service != "ok" {
			t.Fatalf("registered = %+v", result.Registered)
		}
	})

	t.Run("모든 대상이 걸러지면 쿠버네티스를 호출하지 않는다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		targets := []kube.ServiceRef{{Namespace: "ns", Name: "bad", Hostname: "-x"}}

		// when
		result, err := manager.RegisterServices(context.Background(), "ctx", targets, nil)

		// then
		if err != nil || len(result.Failures) != 1 || kubeFake.bulkContext != "" {
			t.Fatalf("err = %v, result = %+v, context seen = %q", err, result, kubeFake.bulkContext)
		}
	})
}

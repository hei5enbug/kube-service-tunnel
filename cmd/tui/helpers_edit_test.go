package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func TestParseEdit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		hostname     string
		port         string
		wantHostname string
		wantPort     int32
	}{
		{"빈 칸은 기본 이름과 자동 포트로 보낸다", "", "", "", 0},
		{"공백만 있는 이름도 빈 칸으로 본다", "   ", "", "", 0},
		{"이름은 소문자로 바꾸고 끝의 점 하나를 뗀다", "API.Local.", "8080", "api.local", 8080},
		{"기본 이름과 같은 이름은 빈 이름으로 보낸다", "WEB.api", "", "", 0},
		{"포트 65535는 받는다", "", "65535", "", 65535},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			web := tunnel("dev", "api", "web")

			// when
			request, _, err := parseEdit(web, tc.hostname, tc.port)

			// then
			if err != nil || request.hostname != tc.wantHostname || request.port != tc.wantPort || request.tunnel != web {
				t.Fatalf("parseEdit = (%+v, %v)", request, err)
			}
		})
	}

	failures := []struct {
		name      string
		hostname  string
		port      string
		wantField editField
		wantError string
	}{
		{"IP 주소인 이름은 Hostname 칸의 오류이다", "10.0.0.1", "", editHostnameField, "hostname must not be an IP address"},
		{"숫자가 아닌 포트는 Local port 칸의 오류이다", "", "8o", editPortField, `local port "8o" is not a number`},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			web := tunnel("dev", "api", "web")

			// when
			_, field, err := parseEdit(web, tc.hostname, tc.port)

			// then
			if err == nil || err.Error() != tc.wantError || field != tc.wantField {
				t.Fatalf("parseEdit = (%v, %v)", field, err)
			}
		})
	}
}

func TestBulkReportBusyPorts(t *testing.T) {
	t.Parallel()
	t.Run("요청한 포트가 바빴던 터널마다 한 줄을 붙이고 성공을 경고로 올린다", func(t *testing.T) {
		t.Parallel()
		// given
		result := dns.BulkRegistrationResult{Registered: []dns.DNSTunnel{fallbackTunnel(), tunnel("dev", "api", "admin")}}
		plan := bulkPlan{Context: "dev", Targets: make([]kube.ServiceRef, 2)}

		// when
		level, text := bulkReport(plan, result, nil)

		// then
		want := "Registered 2 of 2 in dev\nport dev api/web: local port 8080 was busy, using 40002"
		if level != store.LevelWarning || text != want {
			t.Fatalf("bulkReport = (%v, %q)", level, text)
		}
	})

	t.Run("오류가 있으면 바쁜 포트가 있어도 오류 수준이다", func(t *testing.T) {
		t.Parallel()
		// given
		result := dns.BulkRegistrationResult{Registered: []dns.DNSTunnel{fallbackTunnel()}}
		plan := bulkPlan{Context: "dev", Targets: make([]kube.ServiceRef, 2)}

		// when
		level, _ := bulkReport(plan, result, errors.New("shutting down"))

		// then
		if level != store.LevelError {
			t.Fatalf("level = %v, want error", level)
		}
	})
}

func TestTunnelDetailTexts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		tunnel       dns.DNSTunnel
		wantHostname string
		wantPort     string
	}{
		{"기본 이름과 자동 포트를 밝힌다", tunnel("dev", "api", "web"), "web.api (default)", "127.0.0.1:40000 (auto)"},
		{"바꾼 이름과 정한 포트를 밝힌다", withLocalPort(tunnelAt("dev", "api", "web", "api.local"), 8080), "api.local (custom)", "127.0.0.1:8080 (set)"},
		{"정한 포트가 바빴으면 정한 포트와 대신 쓴 포트를 함께 적는다", fallbackTunnel(), "web.api (default)", "127.0.0.1:40002 (set to 8080, which was busy)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			subject := tc.tunnel

			// when
			hostname, port := hostnameText(subject), localPortText(subject)

			// then
			if hostname != tc.wantHostname || port != tc.wantPort {
				t.Fatalf("texts = (%q, %q)", hostname, port)
			}
		})
	}
}

func TestEditHintsAndHelp(t *testing.T) {
	t.Parallel()
	t.Run("Tunnels 힌트는 d delete 앞에 e edit를 둔다", func(t *testing.T) {
		t.Parallel()
		// given
		scope := scopeTunnels

		// when
		hints := hintsFor(scope, 0)

		// then
		if hints[0] != (hint{"e", "edit"}) || hints[1] != (hint{"d", "delete"}) {
			t.Fatalf("hints = %+v", hints)
		}
	})

	t.Run("편집 창 힌트는 좁아도 종료 키를 남긴다", func(t *testing.T) {
		t.Parallel()
		// given
		width := 30

		// when
		hints := hintsFor(scopeEdit, width)

		// then
		if hintsWidth(hints) > width || hints[0].key != "⇥" || hints[len(hints)-1].key != "^C" {
			t.Fatalf("hints = %+v", hints)
		}
	})

	t.Run("도움말은 e 키와 설정이 실행 중에만 남는다는 것과 ○ 표시를 설명한다", func(t *testing.T) {
		t.Parallel()
		// given
		theme := themes[2]

		// when
		text := helpText(theme)

		// then
		for _, want := range []string{"edit the hostname and local port", "settings last until you quit", "○"} {
			if !strings.Contains(text, want) {
				t.Fatalf("help lacks %q:\n%s", want, text)
			}
		}
	})
}

func TestServiceDetailsRegistration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  serviceRow
		want string
	}{
		{"자기 터널이 있으면 그 주소를 적는다", registeredRow(tunnelAt("dev", "api", "web", "api.local")), "yes, http://api.local"},
		{"다른 터널이 이름을 쓰면 그 이름과 컨텍스트를 적는다", serviceRow{
			Context: "dev", Service: httpService("api", "web", 8080), Target: kube.ServiceRef{Namespace: "api", Name: "web"}, NameUser: "prod",
		}, "no, http://web.api is used by context prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			row := tc.row

			// when
			text := detailText(serviceDetails(row))

			// then
			if !strings.Contains(text, "Registered   "+tc.want) {
				t.Fatalf("details lack %q:\n%s", tc.want, text)
			}
		})
	}
}

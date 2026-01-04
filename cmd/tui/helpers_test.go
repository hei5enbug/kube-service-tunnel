package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
		want  string
	}{
		{"폭 안에 들어가면 그대로 둔다", "web", 3, "web"},
		{"넘치면 말줄임표를 포함해 폭에 맞춘다", "website", 5, "webs…"},
		{"폭이 1이면 말줄임표만 남긴다", "website", 1, "…"},
		{"폭이 0이면 빈 문자열이다", "website", 0, ""},
		{"넓은 문자는 두 칸으로 센다", "서비스이름", 5, "서비…"},
		{"넓은 문자를 반으로 자르지 않는다", "서비스이름", 4, "서…"},
		{"합성 이모지를 반으로 자르지 않는다", "a👩‍💻bc", 3, "a…"},
		{"합성 이모지가 들어가면 통째로 남긴다", "a👩‍💻bc", 4, "a👩‍💻…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			text, width := tc.text, tc.width

			// when
			got := truncate(text, width)

			// then
			if got != tc.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", text, width, got, tc.want)
			}
			if displayWidth(got) > max(width, 0) {
				t.Fatalf("width of %q = %d, limit %d", got, displayWidth(got), width)
			}
		})
	}

	t.Run("fit은 자른 뒤 스타일 태그를 이스케이프한다", func(t *testing.T) {
		// given
		text := "[red]ctx"

		// when
		got := fit(text, 20)

		// then
		if got != "[red[]ctx" {
			t.Fatalf("fit = %q", got)
		}
	})
}

func TestMatchFilter(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		fields []string
		want   bool
	}{
		{"빈 질의는 모두와 맞는다", "", []string{"web"}, true},
		{"대소문자를 가리지 않고 부분 문자열을 찾는다", "WE", []string{"Website"}, true},
		{"여러 필드 중 하나만 맞아도 된다", "prod", []string{"http://web.api", "prod"}, true},
		{"어느 필드에도 없으면 맞지 않는다", "db", []string{"web", "dev"}, false},
		{"앞뒤 공백은 무시한다", "  web ", []string{"web"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			query, fields := tc.query, tc.fields

			// when
			got := matchFilter(query, fields...)

			// then
			if got != tc.want {
				t.Fatalf("matchFilter(%q, %v) = %v", query, fields, got)
			}
		})
	}

	t.Run("filterRows는 입력 조각을 바꾸지 않는다", func(t *testing.T) {
		// given
		rows := []string{"a", "b", "a"}

		// when
		kept := filterRows(rows, func(row string) bool { return row == "a" })

		// then
		if !slices.Equal(kept, []string{"a", "a"}) || !slices.Equal(rows, []string{"a", "b", "a"}) {
			t.Fatalf("kept = %v, rows = %v", kept, rows)
		}
	})
}

func registeredRow(tunnel dns.DNSTunnel) serviceRow {
	return serviceRow{Context: tunnel.Context, Service: httpService(tunnel.Namespace, tunnel.Service, 8080), Tunnel: tunnel, Registered: true}
}

func TestServiceMarker(t *testing.T) {
	cases := []struct {
		name       string
		row        serviceRow
		wantSymbol string
		wantNote   string
		wantDim    bool
	}{
		{"등록 가능한 서비스는 표시가 없다", serviceRow{Context: "dev"}, " ", "", false},
		{"기본 이름으로 등록된 서비스는 ●만 붙는다", registeredRow(tunnel("dev", "api", "web")), "●", "", false},
		{"바꾼 이름으로 등록된 서비스는 ●와 그 이름을 적는다", registeredRow(tunnelAt("dev", "api", "web", "api.local")), "●", "→ api.local", false},
		{"다른 터널이 이름을 쓰면 ○와 그 터널의 컨텍스트를 적는다", serviceRow{Context: "dev", NameUser: "prod"}, "○", "in prod", false},
		{"등록할 수 없으면 ✗와 이유를 적고 흐리게 한다", serviceRow{Context: "dev", Reason: kube.ReasonNoSelector}, "✗", "no selector", true},
		{"등록할 수 없어도 다른 터널이 이름을 쓰면 ○가 우선한다", serviceRow{Context: "dev", NameUser: "prod", Reason: kube.ReasonNoHTTPPort}, "○", "in prod", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			row := tc.row

			// when
			symbol, note, dim := serviceMarker(row)

			// then
			if symbol != tc.wantSymbol || note != tc.wantNote || dim != tc.wantDim {
				t.Fatalf("serviceMarker = (%q, %q, %v)", symbol, note, dim)
			}
		})
	}

	t.Run("PORT는 Eligibility가 고른 서비스 포트이고 없으면 -이다", func(t *testing.T) {
		// given
		state := store.State{SelectedContext: "dev", Services: []kube.Service{httpService("api", "web", 8080), grpcService("api", "grpc")}}

		// when
		rows := buildServiceRows(state, indexTunnels([]dns.DNSTunnel{tunnel("prod", "api", "web")}))

		// then
		if rows[0].Port != "8080" || rows[0].NameUser != "prod" || rows[1].Port != "-" || rows[1].Reason != kube.ReasonNoHTTPPort {
			t.Fatalf("rows = %+v", rows)
		}
	})

	t.Run("행은 기억한 설정을 대상에 담고 그 이름을 쓰는 다른 터널을 찾는다", func(t *testing.T) {
		// given
		settings := map[string]store.TunnelSetting{store.SettingKey("dev", "api", "web"): {Hostname: "shop.local", LocalPort: 8081}}
		state := store.State{SelectedContext: "dev", Services: []kube.Service{httpService("api", "web", 8080)}, TunnelSettings: settings}

		// when
		rows := buildServiceRows(state, indexTunnels([]dns.DNSTunnel{tunnelAt("prod", "shop", "cart", "shop.local")}))

		// then
		want := kube.ServiceRef{Namespace: "api", Name: "web", Hostname: "shop.local", LocalPort: 8081}
		if rows[0].Target != want || rows[0].NameUser != "prod" || rows[0].Registered {
			t.Fatalf("row = %+v", rows[0])
		}
	})

	t.Run("이름이 바뀐 자기 터널은 이름이 아니라 서비스로 찾는다", func(t *testing.T) {
		// given
		state := store.State{SelectedContext: "dev", Services: []kube.Service{httpService("api", "web", 8080)}}

		// when
		rows := buildServiceRows(state, indexTunnels([]dns.DNSTunnel{tunnelAt("dev", "api", "web", "api.local")}))

		// then
		if !rows[0].Registered || rows[0].Tunnel.DNSURL != "api.local" || rows[0].NameUser != "" {
			t.Fatalf("row = %+v", rows[0])
		}
	})
}

func TestStatusLine(t *testing.T) {
	cases := []struct {
		name  string
		event store.Event
		width int
		want  string
	}{
		{"이벤트가 없으면 빈 줄이다", store.Event{}, 80, ""},
		{"성공은 ✓ 아이콘만 붙인다", store.Event{ID: 1, Level: store.LevelSuccess, Text: "Copied"}, 80, " ✓ Copied"},
		{"정보는 ℹ 아이콘만 붙인다", store.Event{ID: 1, Level: store.LevelInfo, Text: "Theme: Mono"}, 80, " ℹ Theme: Mono"},
		{"경고는 아이콘과 WARN을 붙인다", store.Event{ID: 1, Level: store.LevelWarning, Text: "lost"}, 80, " ⚠ WARN lost"},
		{"오류는 아이콘과 ERROR를 붙인다", store.Event{ID: 1, Level: store.LevelError, Text: "failed"}, 80, " ✗ ERROR failed"},
		{"태그로 읽힐 문자는 이스케이프한다", store.Event{ID: 1, Level: store.LevelError, Text: "ctx [red] failed"}, 80, " ✗ ERROR ctx [red[] failed"},
		{"여러 줄이면 첫 줄만 보이고 (F2)를 붙인다", store.Event{ID: 1, Level: store.LevelWarning, Text: "first\nsecond"}, 80, " ⚠ WARN first (F2)"},
		{"탭과 캐리지 리턴은 공백으로 바꾼다", store.Event{ID: 1, Level: store.LevelInfo, Text: "a\tb\rc"}, 80, " ℹ a b c"},
		{"넘치면 자르고 (F2)를 붙인다", store.Event{ID: 1, Level: store.LevelSuccess, Text: "Port forwarding started"}, 20, " ✓ Port forwar… (F2)"},
		{"폭을 모르면 자르지 않는다", store.Event{ID: 1, Level: store.LevelSuccess, Text: "long text"}, 0, " ✓ long text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			event, width := tc.event, tc.width

			// when
			got := statusLine(event, width)

			// then
			if got != tc.want {
				t.Fatalf("statusLine = %q, want %q", got, tc.want)
			}
		})
	}
}

// emptyRows joins the first column of every row, which is how a multi-line empty-state text is drawn.
func emptyRows(p *pane) string {
	lines := make([]string, 0, p.table.GetRowCount())
	for row := range p.table.GetRowCount() {
		lines = append(lines, p.table.GetCell(row, 0).Text)
	}
	return strings.Join(lines, "\n")
}

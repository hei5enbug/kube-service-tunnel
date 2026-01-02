package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
)

func TestPlanBulk(t *testing.T) {
	t.Run("후보와 등록 불가와 이미 등록된 수와 후보가 있는 네임스페이스 수를 센다", func(t *testing.T) {
		// given
		resources := sampleCluster()["dev"]
		owners := map[string]string{"web.api": "prod"}

		// when
		plan := planBulk("dev", resources, owners)

		// then
		want := []kube.ServiceRef{{Namespace: "api", Name: "admin"}, {Namespace: "api", Name: "website"}, {Namespace: "default", Name: "hello"}}
		if !slices.Equal(plan.Targets, want) {
			t.Fatalf("targets = %v", plan.Targets)
		}
		if plan.Namespaces != 2 || plan.NotRegistrable != 2 || plan.AlreadyRegistered != 1 {
			t.Fatalf("plan = %+v", plan)
		}
	})

	t.Run("확인 문구는 범위와 건너뛸 수를 밝힌다", func(t *testing.T) {
		// given
		plan := bulkPlan{Context: "dev", Targets: make([]kube.ServiceRef, 23), Namespaces: 12, NotRegistrable: 4, AlreadyRegistered: 2}

		// when
		text := confirmText(plan)

		// then
		want := "Register 23 services in context dev?\n12 namespaces · skipping 4 not registrable, 2 already registered\n" +
			"Uses the list from the last refresh; each service is checked again."
		if text != want {
			t.Fatalf("confirmText = %q", text)
		}
	})

	t.Run("건너뛸 것이 없으면 네임스페이스 수만 적는다", func(t *testing.T) {
		// given
		plan := bulkPlan{Context: "dev", Targets: make([]kube.ServiceRef, 1), Namespaces: 1}

		// when
		text := confirmText(plan)

		// then
		if !strings.HasPrefix(text, "Register 1 service in context dev?\n1 namespace\n") {
			t.Fatalf("confirmText = %q", text)
		}
	})
}

func TestBulkSummary(t *testing.T) {
	failure := kube.ServiceRegistrationFailure{Namespace: "api", Service: "grpc", Reason: "boom"}
	cases := []struct {
		name      string
		result    dns.BulkRegistrationResult
		err       error
		wantLevel store.Level
		wantText  string
	}{
		{"모두 등록되면 성공이다", dns.BulkRegistrationResult{Registered: make([]dns.DNSTunnel, 2)}, nil, store.LevelSuccess, "Registered 2 of 2 in dev"},
		{"건너뛰거나 실패하면 경고이다", dns.BulkRegistrationResult{
			Registered: make([]dns.DNSTunnel, 20), Skipped: make([]kube.ServiceRegistrationFailure, 2), Failures: []kube.ServiceRegistrationFailure{failure},
		}, nil, store.LevelWarning, "Registered 20 of 2 in dev · 2 skipped · 1 failed"},
		{"등록할 수 없게 바뀐 서비스도 센다", dns.BulkRegistrationResult{Ineligible: []kube.ServiceRegistrationFailure{failure}}, nil, store.LevelWarning, "Registered 0 of 2 in dev · 1 not registrable"},
		{"호출이 오류를 돌려주면 오류이다", dns.BulkRegistrationResult{}, errors.New("stop\ndetail"), store.LevelError, "Registered 0 of 2 in dev · error: stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			result, err := tc.result, tc.err

			// when
			level, text := bulkSummary("dev", 2, result, err)

			// then
			if level != tc.wantLevel || text != tc.wantText {
				t.Fatalf("bulkSummary = (%v, %q)", level, text)
			}
		})
	}

	t.Run("보고는 요약 한 줄 뒤에 미리 뺀 서비스와 건너뜀과 등록 불가와 실패와 오류를 한 줄씩 붙인다", func(t *testing.T) {
		// given
		result := dns.BulkRegistrationResult{
			Skipped:    []kube.ServiceRegistrationFailure{{Namespace: "api", Service: "web", Reason: "already registered (context prod)"}},
			Ineligible: []kube.ServiceRegistrationFailure{failure},
			Failures:   []kube.ServiceRegistrationFailure{{Namespace: "api", Service: "auth", Reason: "no ready pod"}},
		}

		// when
		plan := bulkPlan{Context: "dev", Targets: make([]kube.ServiceRef, 3), Excluded: []kube.ServiceRegistrationFailure{
			{Namespace: "api", Service: "ext", Reason: "no selector"},
		}}
		level, text := bulkReport(plan, result, errors.New("partial"))

		// then
		want := strings.Join([]string{
			"Registered 0 of 3 in dev · 1 skipped · 1 not registrable · 1 failed · error: partial · 1 excluded before start",
			"excluded dev api/ext: no selector",
			"skipped dev api/web: already registered (context prod)",
			"not registrable dev api/grpc: boom",
			"failed dev api/auth: no ready pod",
			"error: partial",
		}, "\n")
		if level != store.LevelError || text != want {
			t.Fatalf("bulkReport = (%v, %q)", level, text)
		}
	})

	t.Run("모두 등록되면 보고는 요약 한 줄이다", func(t *testing.T) {
		// given
		result := dns.BulkRegistrationResult{Registered: make([]dns.DNSTunnel, 2)}

		// when
		level, text := bulkReport(bulkPlan{Context: "dev", Targets: make([]kube.ServiceRef, 2)}, result, nil)

		// then
		if level != store.LevelSuccess || text != "Registered 2 of 2 in dev" {
			t.Fatalf("bulkReport = (%v, %q)", level, text)
		}
	})

	t.Run("진행 라벨은 현재 서비스와 순번을 보여 준다", func(t *testing.T) {
		// given
		progress := kube.RegistrationProgress{Done: 3, Total: 23, Current: "web.api"}

		// when
		label := progressLabel("dev", progress)

		// then
		if label != "Registering web.api in dev — 4/23" {
			t.Fatalf("label = %q", label)
		}
	})

	t.Run("마지막 진행 라벨은 마무리 단계를 알린다", func(t *testing.T) {
		// given
		progress := kube.RegistrationProgress{Done: 23, Total: 23}

		// when
		label := progressLabel("dev", progress)

		// then
		if label != "Finishing registration in dev — 23/23" {
			t.Fatalf("label = %q", label)
		}
	})
}

func TestComputeLayout(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		want          layout
	}{
		{"80열에서는 왼쪽 16, Services 31, Tunnels 33이다", 80, 24, layout{mode: layoutNormal, width: 80, height: 24, left: 16, services: 31, tunnels: 33, contextHeight: 4}},
		{"넓어지면 왼쪽은 24까지만 커진다", 160, 40, layout{mode: layoutNormal, width: 160, height: 40, left: 24, services: 67, tunnels: 69, contextHeight: 4}},
		{"80열보다 좁으면 compact이다", 79, 40, layout{mode: layoutCompact, width: 79, height: 40}},
		{"16행보다 낮으면 compact이다", 120, 15, layout{mode: layoutCompact, width: 120, height: 15}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			width, height := tc.width, tc.height

			// when
			got := computeLayout(width, height, 2)

			// then
			if got != tc.want {
				t.Fatalf("computeLayout = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("compact에서는 모든 창이 전체 폭을 쓴다", func(t *testing.T) {
		// given
		compact := computeLayout(60, 20, 2)

		// when
		widths := []int{compact.innerWidth(store.FocusContexts), compact.innerWidth(store.FocusTunnels)}

		// then
		if !slices.Equal(widths, []int{58, 58}) {
			t.Fatalf("inner widths = %v", widths)
		}
	})

}

func TestAllocateWidths(t *testing.T) {
	columns := []column{{natural: 20, minimum: 8}, {natural: 12, minimum: 9}}
	cases := []struct {
		name      string
		available int
		want      []int
	}{
		{"최소 폭을 준 뒤 남는 칸은 앞 열부터 자연 폭까지 준다", 21, []int{12, 9}},
		{"모두 자연 폭에 이르면 남는 칸은 쓰지 않는다", 40, []int{20, 12}},
		{"칸이 모자라면 뒤 열부터 줄인다", 10, []int{8, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			available := tc.available

			// when
			got := allocateWidths(available, columns)

			// then
			if !slices.Equal(got, tc.want) {
				t.Fatalf("allocateWidths(%d) = %v, want %v", available, got, tc.want)
			}
		})
	}
}

func TestHintsFor(t *testing.T) {
	t.Run("좁으면 공통 키를 빼되 도움말과 종료는 남긴다", func(t *testing.T) {
		// given
		width := 50

		// when
		hints := hintsFor(scopeContexts, width)

		// then
		if hintsWidth(hints) > width || hints[len(hints)-1].key != "^C" || hints[len(hints)-2].key != "?" {
			t.Fatalf("hints = %+v", hints)
		}
	})

	t.Run("확인 창 힌트는 좁으면 뒤쪽 키를 빼고 종료 키를 남긴다", func(t *testing.T) {
		// given
		width := 60

		// when
		hints := hintsFor(scopeConfirm, width)

		// then
		if hintsWidth(hints) > width || hints[0].key != "y" || hints[len(hints)-1].key != "^C" {
			t.Fatalf("hints = %+v", hints)
		}
	})

	t.Run("로딩 중에는 종료 키만 보여 준다", func(t *testing.T) {
		// given
		scope := scopeLoading

		// when
		hints := hintsFor(scope, 80)

		// then
		if len(hints) != 1 || hints[0].key != "^C" {
			t.Fatalf("hints = %+v", hints)
		}
	})
}

func TestHistoryAndDetailsText(t *testing.T) {
	t.Run("기록은 시각과 수준과 여러 줄 본문을 그대로 보여 준다", func(t *testing.T) {
		// given
		at := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
		history := []store.Event{{ID: 1, Level: store.LevelError, Text: "boom [x]\nline two", Time: at}}

		// when
		text := historyText(history, themes[2])

		// then
		want := "15:04:05 [-]ERROR[-] boom [x[]\n" + historyIndent + "line two"
		if text != want {
			t.Fatalf("historyText = %q, want %q", text, want)
		}
	})

	t.Run("상세는 이름표를 맞추고 값의 다음 줄을 들여 쓴다", func(t *testing.T) {
		// given
		details := []detail{{"Context", "dev"}, {"Discovery", "failed\nforbidden"}}

		// when
		text := detailText(details)

		// then
		want := "Context    dev\nDiscovery  failed\n           forbidden"
		if text != want {
			t.Fatalf("detailText = %q, want %q", text, want)
		}
	})

	t.Run("포트 목록은 이름 없는 포트와 이름 있는 targetPort도 보여 준다", func(t *testing.T) {
		// given
		ports := []kube.ServicePort{{Port: 80, TargetPortName: "http"}, {Name: "grpc", Port: 50051, TargetPort: 9000, Protocol: "UDP"}}

		// when
		text := portLines(ports)

		// then
		want := "(unnamed): port 80, targetPort http, TCP\ngrpc: port 50051, targetPort 9000, UDP"
		if text != want {
			t.Fatalf("portLines = %q", text)
		}
	})

	t.Run("선택자는 키 순서로 보여 준다", func(t *testing.T) {
		// given
		selector := map[string]string{"tier": "api", "app": "web"}

		// when
		text := selectorText(selector)

		// then
		if text != "app=web, tier=api" {
			t.Fatalf("selectorText = %q", text)
		}
	})
}

func TestSmallHelpers(t *testing.T) {
	colorCases := []struct {
		name  string
		color tcell.Color
		want  string
	}{
		{"기본 색은 보기의 글자색으로 되돌리는 태그가 된다", tcell.ColorDefault, "[-]"},
		{"RGB 색은 16진수 태그가 된다", tcell.NewHexColor(0x0a0b0c), "[#0a0b0c]"},
	}
	for _, tc := range colorCases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			color := tc.color

			// when
			tag := colorTag(color)

			// then
			if tag != tc.want {
				t.Fatalf("colorTag = %q, want %q", tag, tc.want)
			}
		})
	}

	neighborCases := []struct {
		name string
		from store.FocusArea
		step int
		want store.FocusArea
	}{
		{"첫 창에서 뒤로 가면 마지막 창이다", store.FocusContexts, -1, store.FocusTunnels},
		{"마지막 창에서 앞으로 가면 첫 창이다", store.FocusTunnels, 1, store.FocusContexts},
	}
	for _, tc := range neighborCases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			from, step := tc.from, tc.step

			// when
			got := neighborArea(from, step)

			// then
			if got != tc.want {
				t.Fatalf("neighborArea = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("탭 줄은 포커스된 창을 괄호로 감싼다", func(t *testing.T) {
		// given
		focus := store.FocusNamespaces

		// when
		strip := tabStrip(focus)

		// then
		if strip != "1 Context [2 Namespace] 3 Services 4 Tunnels" {
			t.Fatalf("tabStrip = %q", strip)
		}
	})

	t.Run("상태 변화가 없으면 다시 그리지 않는다", func(t *testing.T) {
		// given
		state := store.State{SelectedContext: "dev", TunnelsRevision: 3}

		// when
		change := diffState(state, state)

		// then
		if change.any() {
			t.Fatalf("change = %+v, want none", change)
		}
	})
}

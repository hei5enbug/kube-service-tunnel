package tui

import (
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func TestStartDiscovery(t *testing.T) {
	t.Run("이미 로딩 중이면 컨텍스트 탐색을 다시 시작하지 않는다", func(t *testing.T) {
		// given
		running := startTestApp(t, withKube(&fakeKube{current: "dev", resources: sampleCluster()}))
		running.app.store.TryBeginLoading("busy")

		// when
		running.app.start()

		// then
		time.Sleep(settleDelay)
		if got := running.kube.fetchAllCalls(); got != 0 {
			t.Fatalf("fetch calls = %d, want 0", got)
		}
		if got := running.app.store.GetState().LoadingLabel; got != "busy" {
			t.Fatalf("label = %q, want busy", got)
		}
	})

	t.Run("컨텍스트가 하나도 없으면 경고로 알린다", func(t *testing.T) {
		// given
		empty := &fakeKube{resources: map[string]kube.ContextResources{}}

		// when
		running := startLoadedApp(t, withKube(empty))

		// then
		status := running.app.store.GetState().Status
		if status.Level != store.LevelWarning || status.Text != "No contexts found in the kubeconfig" {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("모든 컨텍스트를 읽으면 개수와 함께 성공으로 알린다", func(t *testing.T) {
		// given
		healthy := &fakeKube{current: "dev", resources: map[string]kube.ContextResources{
			"dev": {Namespaces: map[string][]kube.Service{"api": {httpService("api", "web", 8080)}}},
		}}

		// when
		running := startLoadedApp(t, withKube(healthy))

		// then
		status := running.app.store.GetState().Status
		if status.Level != store.LevelSuccess || status.Text != "Loaded 1 context" {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestRefreshGuards(t *testing.T) {
	t.Run("활성 컨텍스트가 없으면 r 키는 새로 고치지 않고 알리기만 한다", func(t *testing.T) {
		// given
		running := startTestApp(t)

		// when
		running.typeText("r")

		// then
		running.waitStatus(t, "No active context to refresh")
		if got := running.kube.refreshRuns(); got != 0 {
			t.Fatalf("refresh calls = %d, want 0", got)
		}
	})

	t.Run("다른 작업이 진행 중이면 새로 고침을 시작하지 않는다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.app.store.TryBeginLoading("busy")

		// when
		running.onUI(t, running.app.refreshActiveContext)

		// then
		time.Sleep(settleDelay)
		if got := running.kube.refreshRuns(); got != 0 {
			t.Fatalf("refresh calls = %d, want 0", got)
		}
		if got := running.app.store.GetState().LoadingLabel; got != "busy" {
			t.Fatalf("label = %q, want busy", got)
		}
	})
}

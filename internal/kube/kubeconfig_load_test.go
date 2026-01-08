package kube

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeRawKubeconfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func TestDefaultKubeconfigPath_HomeUnavailable(t *testing.T) {
	t.Run("홈 디렉터리를 알 수 없으면 오류를 낸다", func(t *testing.T) {
		// given
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root process")
		}
		t.Setenv("HOME", "")

		// when
		got, err := DefaultKubeconfigPath()

		// then
		if err == nil || got != "" {
			t.Fatalf("DefaultKubeconfigPath() = %q, %v, want error", got, err)
		}
	})
}

func TestNewContextClient(t *testing.T) {
	t.Run("경로가 비어 있으면 기본 경로의 kubeconfig를 사용한다", func(t *testing.T) {
		// given
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root process")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(sudoUserEnv, "")
		defaultPath := filepath.Join(home, ".kube", "config")
		if err := os.MkdirAll(filepath.Dir(defaultPath), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(defaultPath, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}

		// when
		client, err := NewContextClient("")

		// then
		if err != nil || client.kubeconfigPath != defaultPath {
			t.Fatalf("client = %+v, err = %v, want path %q", client, err, defaultPath)
		}
	})

	t.Run("경로가 비어 있고 홈 디렉터리를 알 수 없으면 오류를 낸다", func(t *testing.T) {
		// given
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root process")
		}
		t.Setenv("HOME", "")

		// when
		client, err := NewContextClient("")

		// then
		if err == nil || client != nil {
			t.Fatalf("client = %+v, err = %v, want error", client, err)
		}
	})

	t.Run("kubeconfig 파일이 없으면 경로를 담은 오류를 낸다", func(t *testing.T) {
		// given
		missing := filepath.Join(t.TempDir(), "missing")

		// when
		client, err := NewContextClient(missing)

		// then
		if err == nil || client != nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("client = %+v, err = %v, want not found error naming %q", client, err, missing)
		}
	})
}

func TestContextClient_ListContexts(t *testing.T) {
	t.Run("kubeconfig의 컨텍스트를 이름순으로 클러스터와 사용자와 함께 돌려준다", func(t *testing.T) {
		// given
		path := writeKubeconfig(t, "dev", map[string]string{"prod": "http://127.0.0.1:1", "dev": "http://127.0.0.1:2"})
		client, err := NewContextClient(path)
		if err != nil {
			t.Fatalf("NewContextClient() error = %v", err)
		}

		// when
		got, err := client.ListContexts(context.Background())

		// then
		want := []Context{
			{Name: "dev", Cluster: "cluster-dev", User: "user-dev"},
			{Name: "prod", Cluster: "cluster-prod", User: "user-prod"},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("ListContexts() = %+v, %v, want %+v", got, err, want)
		}
	})

	t.Run("컨텍스트가 없는 kubeconfig는 빈 목록을 돌려준다", func(t *testing.T) {
		// given
		client, err := NewContextClient(writeRawKubeconfig(t, "apiVersion: v1\nkind: Config\n"))
		if err != nil {
			t.Fatalf("NewContextClient() error = %v", err)
		}

		// when
		got, err := client.ListContexts(context.Background())

		// then
		if err != nil || len(got) != 0 {
			t.Fatalf("ListContexts() = %+v, %v, want empty", got, err)
		}
	})

	t.Run("kubeconfig가 손상되었으면 오류를 낸다", func(t *testing.T) {
		// given
		client, err := NewContextClient(writeRawKubeconfig(t, "clusters: [unterminated"))
		if err != nil {
			t.Fatalf("NewContextClient() error = %v", err)
		}

		// when
		got, err := client.ListContexts(context.Background())

		// then
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig file") || got != nil {
			t.Fatalf("ListContexts() = %+v, %v, want load error", got, err)
		}
	})
}

func TestContextClient_GetCurrentContext(t *testing.T) {
	t.Run("kubeconfig의 현재 컨텍스트 이름을 돌려준다", func(t *testing.T) {
		// given
		client, err := NewContextClient(writeKubeconfig(t, "prod", map[string]string{"prod": "http://127.0.0.1:1", "dev": "http://127.0.0.1:2"}))
		if err != nil {
			t.Fatalf("NewContextClient() error = %v", err)
		}

		// when
		got, err := client.GetCurrentContext(context.Background())

		// then
		if err != nil || got != "prod" {
			t.Fatalf("GetCurrentContext() = %q, %v, want prod", got, err)
		}
	})

	t.Run("kubeconfig가 손상되었으면 오류를 낸다", func(t *testing.T) {
		// given
		client, err := NewContextClient(writeRawKubeconfig(t, "clusters: [unterminated"))
		if err != nil {
			t.Fatalf("NewContextClient() error = %v", err)
		}

		// when
		got, err := client.GetCurrentContext(context.Background())

		// then
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig file") || got != "" {
			t.Fatalf("GetCurrentContext() = %q, %v, want load error", got, err)
		}
	})
}

func TestLoadKubeconfigWithContext(t *testing.T) {
	servers := map[string]string{"dev": "http://127.0.0.1:11111", "prod": "http://127.0.0.1:22222"}

	tests := []struct {
		name        string
		contextName string
		wantHost    string
	}{
		{"컨텍스트 이름이 비어 있으면 현재 컨텍스트의 서버를 쓴다", "", "http://127.0.0.1:11111"},
		{"컨텍스트 이름을 지정하면 그 컨텍스트의 서버를 쓴다", "prod", "http://127.0.0.1:22222"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			path := writeKubeconfig(t, "dev", servers)

			// when
			config, err := LoadKubeconfigWithContext(path, tt.contextName)

			// then
			if err != nil || config.Host != tt.wantHost {
				t.Fatalf("LoadKubeconfigWithContext() = %+v, %v, want host %q", config, err, tt.wantHost)
			}
		})
	}

	t.Run("loadKubeconfig는 현재 컨텍스트를 사용한다", func(t *testing.T) {
		// given
		path := writeKubeconfig(t, "prod", servers)

		// when
		config, err := loadKubeconfig(path)

		// then
		if err != nil || config.Host != servers["prod"] {
			t.Fatalf("loadKubeconfig() = %+v, %v, want host %q", config, err, servers["prod"])
		}
	})

	t.Run("경로가 비어 있으면 기본 경로의 kubeconfig를 읽는다", func(t *testing.T) {
		// given
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root process")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(sudoUserEnv, "")
		source := writeKubeconfig(t, "dev", servers)
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read kubeconfig: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(home, ".kube"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, ".kube", "config"), data, 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}

		// when
		config, err := LoadKubeconfigWithContext("", "")

		// then
		if err != nil || config.Host != servers["dev"] {
			t.Fatalf("LoadKubeconfigWithContext() = %+v, %v, want host %q", config, err, servers["dev"])
		}
	})

	errorTests := []struct {
		name    string
		path    func(t *testing.T) string
		context string
		wantErr string
	}{
		{"경로가 비어 있고 홈 디렉터리를 알 수 없으면 오류를 낸다", func(t *testing.T) string {
			t.Setenv("HOME", "")
			return ""
		}, "", "home directory"},
		{"kubeconfig 파일이 없으면 오류를 낸다", func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "missing")
		}, "", "kubeconfig file not found"},
		{"kubeconfig가 손상되었으면 오류를 낸다", func(t *testing.T) string {
			return writeRawKubeconfig(t, "clusters: [unterminated")
		}, "", "build config from flags"},
		{"없는 컨텍스트를 지정하면 오류를 낸다", func(t *testing.T) string {
			return writeKubeconfig(t, "dev", servers)
		}, "ghost", "build config from flags"},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			if os.Geteuid() == 0 {
				t.Skip("requires a non-root process")
			}
			path := tt.path(t)

			// when
			config, err := LoadKubeconfigWithContext(path, tt.context)

			// then
			if err == nil || config != nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadKubeconfigWithContext() = %+v, %v, want error containing %q", config, err, tt.wantErr)
			}
		})
	}
}

func TestNewClientForContext(t *testing.T) {
	t.Run("지정한 컨텍스트의 서버를 가리키는 설정과 클라이언트를 만든다", func(t *testing.T) {
		// given
		path := writeKubeconfig(t, "dev", map[string]string{"dev": "http://127.0.0.1:11111", "prod": "http://127.0.0.1:22222"})

		// when
		config, clientset, err := newClientForContext(path, "prod")

		// then
		if err != nil || clientset == nil || config.Host != "http://127.0.0.1:22222" {
			t.Fatalf("newClientForContext() = %+v, %v, %v, want prod host and a client", config, clientset, err)
		}
	})

	t.Run("kubeconfig를 읽지 못하면 로드 오류를 낸다", func(t *testing.T) {
		// given
		missing := filepath.Join(t.TempDir(), "missing")

		// when
		config, clientset, err := newClientForContext(missing, "dev")

		// then
		if err == nil || config != nil || clientset != nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("newClientForContext() = %+v, %v, %v, want load error", config, clientset, err)
		}
	})

	t.Run("설정으로 클라이언트를 만들 수 없으면 생성 오류를 낸다", func(t *testing.T) {
		// given
		raw := "apiVersion: v1\nkind: Config\ncurrent-context: dev\n" +
			"clusters:\n- name: c\n  cluster:\n    server: https://127.0.0.1:1\n    certificate-authority-data: bm90IGEgY2VydGlmaWNhdGU=\n" +
			"users:\n- name: u\n  user: {}\n" +
			"contexts:\n- name: dev\n  context:\n    cluster: c\n    user: u\n"
		path := writeRawKubeconfig(t, raw)

		// when
		config, clientset, err := newClientForContext(path, "dev")

		// then
		if err == nil || config != nil || clientset != nil || !strings.Contains(err.Error(), "create kubernetes client") {
			t.Fatalf("newClientForContext() = %+v, %v, %v, want create error", config, clientset, err)
		}
	})
}

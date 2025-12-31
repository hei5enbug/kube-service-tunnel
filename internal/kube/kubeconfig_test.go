package kube

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestResolveHomeDir(t *testing.T) {
	fallback := func() (string, error) { return "/root-home", nil }
	lookupOK := func(name string) (*user.User, error) {
		return &user.User{Username: name, HomeDir: "/home/" + name}, nil
	}
	lookupFail := func(name string) (*user.User, error) { return nil, errors.New("unknown user") }
	lookupMustNotRun := func(name string) (*user.User, error) {
		t.Errorf("lookup(%q) must not be called", name)
		return nil, errors.New("unexpected lookup")
	}

	tests := []struct {
		name     string
		euid     int
		sudoUser string
		lookup   userLookupFunc
		want     string
	}{
		{"루트 권한으로 sudo를 실행하면 SUDO_USER의 홈 디렉터리를 쓴다", 0, "alice", lookupOK, "/home/alice"},
		{"SUDO_USER가 root이면 기본 홈 디렉터리를 쓴다", 0, "root", lookupMustNotRun, "/root-home"},
		{"SUDO_USER가 비어 있으면 기본 홈 디렉터리를 쓴다", 0, "", lookupMustNotRun, "/root-home"},
		{"루트가 아니면 SUDO_USER가 있어도 기본 홈 디렉터리를 쓴다", 1000, "alice", lookupMustNotRun, "/root-home"},
		{"사용자 조회에 실패하면 기본 홈 디렉터리로 대체한다", 0, "ghost", lookupFail, "/root-home"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			euid, sudoUser, lookup := tt.euid, tt.sudoUser, tt.lookup

			// when
			got, err := resolveHomeDir(euid, sudoUser, lookup, fallback)

			// then
			if err != nil || got != tt.want {
				t.Fatalf("resolveHomeDir() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestDefaultKubeconfigPath(t *testing.T) {
	t.Run("루트가 아닌 프로세스는 SUDO_USER와 무관하게 자신의 홈 아래 kube config를 가리킨다", func(t *testing.T) {
		// given
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root process")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv(sudoUserEnv, "someone-else")

		// when
		got, err := DefaultKubeconfigPath()

		// then
		want := filepath.Join(home, ".kube", "config")
		if err != nil || got != want {
			t.Fatalf("DefaultKubeconfigPath() = %q, %v, want %q", got, err, want)
		}
	})
}

func TestNewContextClient_ExplicitPath(t *testing.T) {
	t.Run("명시한 kubeconfig 경로를 기본 경로보다 우선한다", func(t *testing.T) {
		// given
		t.Setenv("HOME", t.TempDir())
		explicit := filepath.Join(t.TempDir(), "custom-config")
		if err := os.WriteFile(explicit, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}

		// when
		client, err := NewContextClient(explicit)

		// then
		if err != nil || client.kubeconfigPath != explicit {
			t.Fatalf("client = %+v, err = %v, want path %q", client, err, explicit)
		}
	})
}

package clipboard

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestPasteboardCommand(t *testing.T) {
	plain := Command{Path: pbcopyPath}
	cases := []struct {
		name string
		euid int
		env  map[string]string
		want Command
	}{
		{
			name: "sudo로 실행되면 호출한 사용자의 세션에서 pbcopy를 실행한다",
			euid: 0,
			env:  map[string]string{"SUDO_UID": "501", "SUDO_USER": "alice"},
			want: Command{Path: launchctl, Args: []string{"asuser", "501", sudoPath, "-n", "-u", "alice", pbcopyPath}},
		},
		{name: "root가 아니면 pbcopy를 직접 실행한다", euid: 501, env: map[string]string{"SUDO_UID": "501", "SUDO_USER": "alice"}, want: plain},
		{name: "SUDO_UID가 숫자가 아니면 pbcopy를 직접 실행한다", euid: 0, env: map[string]string{"SUDO_UID": "5x1", "SUDO_USER": "alice"}, want: plain},
		{name: "SUDO_UID가 없으면 pbcopy를 직접 실행한다", euid: 0, env: map[string]string{"SUDO_USER": "alice"}, want: plain},
		{name: "SUDO_USER가 비어 있으면 pbcopy를 직접 실행한다", euid: 0, env: map[string]string{"SUDO_UID": "501"}, want: plain},
		{name: "SUDO_USER가 root이면 pbcopy를 직접 실행한다", euid: 0, env: map[string]string{"SUDO_UID": "0", "SUDO_USER": "root"}, want: plain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			getenv := env(tc.env)

			// when
			got := PasteboardCommand(tc.euid, getenv)

			// then
			if got.Path != tc.want.Path || !slices.Equal(got.Args, tc.want.Args) {
				t.Fatalf("command = %+v, want %+v", got, tc.want)
			}
		})
	}
}

type recordedRun struct {
	command  Command
	stdin    string
	deadline time.Time
	hasLimit bool
}

func recordingSystem(goos string, result error, record *recordedRun) *System {
	return &System{
		goos:    goos,
		command: Command{Path: pbcopyPath},
		timeout: CopyTimeout,
		run: func(ctx context.Context, command Command, stdin string) error {
			record.command, record.stdin = command, stdin
			record.deadline, record.hasLimit = ctx.Deadline()
			return result
		},
	}
}

func TestSystemCopy(t *testing.T) {
	t.Run("macOS에서는 텍스트를 표준 입력으로 넘기고 3초 제한을 건다", func(t *testing.T) {
		// given
		var record recordedRun
		system := recordingSystem(darwinGOOS, nil, &record)
		start := time.Now()

		// when
		err := system.Copy(context.Background(), "http://web.api")

		// then
		if err != nil {
			t.Fatalf("Copy() error = %v", err)
		}
		if record.stdin != "http://web.api" || record.command.Path != pbcopyPath {
			t.Fatalf("run = %+v", record)
		}
		if !record.hasLimit || record.deadline.Sub(start) > CopyTimeout+time.Second {
			t.Fatalf("deadline = %v, has = %v", record.deadline, record.hasLimit)
		}
	})

	t.Run("명령이 실패하면 그 오류를 돌려준다", func(t *testing.T) {
		// given
		var record recordedRun
		failure := errors.New("no session")
		system := recordingSystem(darwinGOOS, failure, &record)

		// when
		err := system.Copy(context.Background(), "x")

		// then
		if !errors.Is(err, failure) {
			t.Fatalf("Copy() error = %v, want %v", err, failure)
		}
	})

	t.Run("macOS가 아니면 명령을 실행하지 않고 ErrUnsupported를 돌려준다", func(t *testing.T) {
		// given
		var record recordedRun
		system := recordingSystem("linux", nil, &record)

		// when
		err := system.Copy(context.Background(), "x")

		// then
		if !errors.Is(err, ErrUnsupported) || record.command.Path != "" {
			t.Fatalf("Copy() error = %v, run = %+v", err, record)
		}
	})

	t.Run("NewSystem은 현재 플랫폼과 3초 제한으로 만든다", func(t *testing.T) {
		// given
		want := CopyTimeout

		// when
		system := NewSystem()

		// then
		if system.timeout != want || system.goos == "" || system.command.Path == "" || system.run == nil {
			t.Fatalf("system = %+v", system)
		}
	})
}

func TestRunCommand(t *testing.T) {
	t.Run("셸 없이 실행하고 표준 입력을 넘긴다", func(t *testing.T) {
		// given
		path, err := exec.LookPath("cat")
		if err != nil {
			t.Skip("cat is not available")
		}

		// when
		err = runCommand(context.Background(), Command{Path: path}, "hello")

		// then
		if err != nil {
			t.Fatalf("runCommand() error = %v", err)
		}
	})

	t.Run("실패한 명령은 경로를 담은 오류를 돌려준다", func(t *testing.T) {
		// given
		path, err := exec.LookPath("false")
		if err != nil {
			t.Skip("false is not available")
		}

		// when
		err = runCommand(context.Background(), Command{Path: path}, "")

		// then
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("runCommand() error = %v, want exit error", err)
		}
	})

	t.Run("출력이 있는 실패는 출력을 오류에 덧붙인다", func(t *testing.T) {
		// given
		path, err := exec.LookPath("ls")
		if err != nil {
			t.Skip("ls is not available")
		}

		// when
		err = runCommand(context.Background(), Command{Path: path, Args: []string{"/nonexistent-clipboard-path"}}, "")

		// then
		if err == nil || len(err.Error()) <= len("run "+path+": exit status 1") {
			t.Fatalf("runCommand() error = %v, want output detail", err)
		}
	})
}

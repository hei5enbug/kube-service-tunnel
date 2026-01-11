package host

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

var (
	errInjectedRename = errors.New("injected rename failure")
	errInjectedWrite  = errors.New("injected write failure")
	errInjectedStep   = errors.New("injected step failure")
	errInjectedRestor = errors.New("injected restore failure")
)

const shortContent = "127.0.0.1\tnew.local\n"

// faultyTemp wraps a real temp file and fails selected operations.
type faultyTemp struct {
	*os.File
	writeErr, syncErr, chmodErr, chownErr, closeErr error
	chownCalls                                      [][2]int
}

func (f *faultyTemp) WriteString(s string) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.File.WriteString(s)
}

// Sync skips the real fsync, which is slow on some filesystems and irrelevant to what the tests assert.
func (f *faultyTemp) Sync() error {
	return f.syncErr
}

func (f *faultyTemp) Chmod(mode os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.File.Chmod(mode)
}

func (f *faultyTemp) Chown(uid, gid int) error {
	f.chownCalls = append(f.chownCalls, [2]int{uid, gid})
	return f.chownErr
}

func (f *faultyTemp) Close() error {
	realErr := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return realErr
}

// rewriteFaults describes how one in-place open misbehaves.
type rewriteFaults struct {
	openErr, writeErr, truncateErr, syncErr, closeErr error
	partialWrite                                      bool
}

type faultyRewrite struct {
	*os.File
	faults rewriteFaults
}

func (f *faultyRewrite) WriteAt(b []byte, off int64) (int, error) {
	if f.faults.writeErr == nil {
		return f.File.WriteAt(b, off)
	}
	if f.faults.partialWrite && len(b) > 1 {
		n, _ := f.File.WriteAt(b[:len(b)/2], off)
		return n, f.faults.writeErr
	}
	return 0, f.faults.writeErr
}

func (f *faultyRewrite) Truncate(size int64) error {
	if f.faults.truncateErr != nil {
		return f.faults.truncateErr
	}
	return f.File.Truncate(size)
}

// Sync skips the real fsync, which is slow on some filesystems and irrelevant to what the tests assert.
func (f *faultyRewrite) Sync() error {
	return f.faults.syncErr
}

func (f *faultyRewrite) Close() error {
	realErr := f.File.Close()
	if f.faults.closeErr != nil {
		return f.faults.closeErr
	}
	return realErr
}

// withRewriteOpens returns ops whose n-th in-place open uses the n-th faults entry; later opens are healthy.
func withRewriteOpens(ops fileOps, faults ...rewriteFaults) (fileOps, *int) {
	attempts := 0
	ops.openForRewrite = func(path string) (rewriteFile, error) {
		var current rewriteFaults
		if attempts < len(faults) {
			current = faults[attempts]
		}
		attempts++
		if current.openErr != nil {
			return nil, current.openErr
		}
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return nil, err
		}
		return &faultyRewrite{File: f, faults: current}, nil
	}
	return ops, &attempts
}

// withRenameFailure returns ops whose rename always fails and records the destinations it was asked for.
func withRenameFailure(ops fileOps) (fileOps, *[]string) {
	var destinations []string
	ops.rename = func(oldpath, newpath string) error {
		destinations = append(destinations, newpath)
		return errInjectedRename
	}
	return ops, &destinations
}

// withFaultyTemp returns ops whose temp files fail as configured by build.
func withFaultyTemp(ops fileOps, build func(*faultyTemp)) fileOps {
	ops.createTemp = func(dir, pattern string) (tempFile, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		tmp := &faultyTemp{File: f}
		build(tmp)
		return tmp, nil
	}
	return ops
}

// withUID returns ops reporting uid as the effective user.
func withUID(ops fileOps, uid int) fileOps {
	ops.effectiveUID = func() int { return uid }
	return ops
}

func hostsFixture(t *testing.T, content string) (string, os.FileInfo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts")
	writeFile(t, path, content, 0o640)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	return path, info
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

type stubInfo struct{ os.FileInfo }

func (stubInfo) Sys() any { return nil }

func TestWriteTempBesideErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		uid     int
		build   func(*faultyTemp)
		wantErr error
		wantMsg string
	}{
		{"임시 파일 쓰기가 실패하면 오류를 반환한다", 1000, func(f *faultyTemp) { f.writeErr = errInjectedStep }, errInjectedStep, "write temp"},
		{"임시 파일 동기화가 실패하면 오류를 반환한다", 1000, func(f *faultyTemp) { f.syncErr = errInjectedStep }, errInjectedStep, "sync temp"},
		{"임시 파일 권한 변경이 실패하면 오류를 반환한다", 1000, func(f *faultyTemp) { f.chmodErr = errInjectedStep }, errInjectedStep, "chmod temp"},
		{"루트 권한에서 소유자 변경이 실패하면 오류를 반환한다", 0, func(f *faultyTemp) { f.chownErr = errInjectedStep }, errInjectedStep, "chown temp"},
		{"임시 파일 닫기가 실패하면 오류를 반환한다", 1000, func(f *faultyTemp) { f.closeErr = errInjectedStep }, errInjectedStep, "close temp"},
	}

	for _, tc := range cases {
		t.Run(tc.name+"이고 임시 파일을 지운다", func(t *testing.T) {
			t.Parallel()

			// given
			path, info := hostsFixture(t, baseHosts)
			ops := withUID(withFaultyTemp(defaultFileOps(), tc.build), tc.uid)

			// when
			tmpPath, err := ops.writeTempBeside(path, info, shortContent)

			// then
			if !errors.Is(err, tc.wantErr) || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want %q wrapping %v", err, tc.wantMsg, tc.wantErr)
			}
			if tmpPath != "" {
				t.Fatalf("tmpPath = %q, want empty", tmpPath)
			}
			if names := dirNames(t, filepath.Dir(path)); len(names) != 1 || names[0] != "hosts" {
				t.Fatalf("directory entries = %v, want only hosts", names)
			}
		})
	}

	t.Run("임시 파일 생성이 실패하면 오류를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		path, info := hostsFixture(t, baseHosts)
		ops := defaultFileOps()
		ops.createTemp = func(string, string) (tempFile, error) { return nil, errInjectedStep }

		// when
		_, err := ops.writeTempBeside(path, info, shortContent)

		// then
		if !errors.Is(err, errInjectedStep) || !strings.Contains(err.Error(), "create temp") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFillTempChown(t *testing.T) {
	t.Parallel()

	t.Run("루트 권한이면 원본 파일의 소유자와 그룹으로 변경한다", func(t *testing.T) {
		t.Parallel()

		// given
		_, info := hostsFixture(t, baseHosts)
		stat := info.Sys().(*syscall.Stat_t)
		ops := withUID(defaultFileOps(), 0)
		file, err := os.CreateTemp(t.TempDir(), "tmp-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		tmp := &faultyTemp{File: file}
		defer tmp.Close()

		// when
		err = ops.fillTemp(tmp, info, shortContent)

		// then
		if err != nil {
			t.Fatalf("fillTemp error: %v", err)
		}
		want := [2]int{int(stat.Uid), int(stat.Gid)}
		if len(tmp.chownCalls) != 1 || tmp.chownCalls[0] != want {
			t.Fatalf("chown calls = %v, want [%v]", tmp.chownCalls, want)
		}
	})

	t.Run("루트가 아니면 소유자를 바꾸지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		_, info := hostsFixture(t, baseHosts)
		ops := withUID(defaultFileOps(), 501)
		file, err := os.CreateTemp(t.TempDir(), "tmp-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		tmp := &faultyTemp{File: file}
		defer tmp.Close()

		// when
		err = ops.fillTemp(tmp, info, shortContent)

		// then
		if err != nil || len(tmp.chownCalls) != 0 {
			t.Fatalf("err = %v, chown calls = %v", err, tmp.chownCalls)
		}
	})

	t.Run("루트여도 소유자 정보를 알 수 없으면 소유자를 바꾸지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		_, info := hostsFixture(t, baseHosts)
		ops := withUID(defaultFileOps(), 0)
		file, err := os.CreateTemp(t.TempDir(), "tmp-*")
		if err != nil {
			t.Fatalf("create temp: %v", err)
		}
		tmp := &faultyTemp{File: file}
		defer tmp.Close()

		// when
		err = ops.fillTemp(tmp, stubInfo{info}, shortContent)

		// then
		if err != nil || len(tmp.chownCalls) != 0 {
			t.Fatalf("err = %v, chown calls = %v", err, tmp.chownCalls)
		}
	})
}

func TestWriteHostsFileFallback(t *testing.T) {
	t.Parallel()

	original := baseHosts + dockerBlock

	t.Run("rename이 실패하면 제자리 쓰기로 새 내용을 쓰고 권한과 임시 파일 정리를 지킨다", func(t *testing.T) {
		t.Parallel()

		// given
		path, _ := hostsFixture(t, original)
		ops, _ := withRenameFailure(defaultFileOps())

		// when
		err := ops.writeHostsFile(path, shortContent, []byte(original))

		// then
		if err != nil {
			t.Fatalf("writeHostsFile error: %v", err)
		}
		if got := readFile(t, path); got != shortContent {
			t.Fatalf("content = %q, want %q", got, shortContent)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
		}
		if names := dirNames(t, filepath.Dir(path)); len(names) != 1 {
			t.Fatalf("directory entries = %v, want only hosts", names)
		}
	})

	t.Run("제자리 쓰기를 파일을 비우지 않고 연다", func(t *testing.T) {
		t.Parallel()

		// given
		path, _ := hostsFixture(t, original)

		// when
		f, err := defaultFileOps().openForRewrite(path)

		// then
		if err != nil {
			t.Fatalf("open error: %v", err)
		}
		defer f.Close()
		if got := readFile(t, path); got != original {
			t.Fatalf("content after open = %q, want unchanged", got)
		}
	})

	failures := []struct {
		name   string
		faults rewriteFaults
		cause  error
		msg    string
	}{
		{"쓰기 도중", rewriteFaults{writeErr: errInjectedWrite, partialWrite: true}, errInjectedWrite, "write hosts file"},
		{"잘라내기", rewriteFaults{truncateErr: errInjectedWrite}, errInjectedWrite, "truncate hosts file"},
		{"동기화", rewriteFaults{syncErr: errInjectedWrite}, errInjectedWrite, "sync hosts file"},
		{"닫기", rewriteFaults{closeErr: errInjectedWrite}, errInjectedWrite, "close hosts file"},
		{"열기", rewriteFaults{openErr: errInjectedWrite}, errInjectedWrite, "open hosts file"},
	}

	for _, tc := range failures {
		t.Run("제자리 쓰기의 "+tc.name+"가 실패하면 원본을 복구하고 복구했다고 알린다", func(t *testing.T) {
			t.Parallel()

			// given
			path, _ := hostsFixture(t, original)
			ops, attempts := withRewriteOpens(defaultFileOps(), tc.faults)
			ops, _ = withRenameFailure(ops)

			// when
			err := ops.writeHostsFile(path, shortContent, []byte(original))

			// then
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, errInjectedRename) || !errors.Is(err, tc.cause) {
				t.Fatalf("err = %v, want wrapping rename and write errors", err)
			}
			if !strings.Contains(err.Error(), tc.msg) || !strings.HasSuffix(err.Error(), "original content restored") {
				t.Fatalf("err = %q", err)
			}
			if got := readFile(t, path); got != original {
				t.Fatalf("content = %q, want original", got)
			}
			if *attempts != 2 {
				t.Fatalf("opens = %d, want 2", *attempts)
			}
		})
	}

	restoreFailures := []struct {
		name    string
		restore rewriteFaults
	}{
		{"열기", rewriteFaults{openErr: errInjectedRestor}},
		{"쓰기", rewriteFaults{writeErr: errInjectedRestor}},
		{"잘라내기", rewriteFaults{truncateErr: errInjectedRestor}},
		{"동기화", rewriteFaults{syncErr: errInjectedRestor}},
		{"닫기", rewriteFaults{closeErr: errInjectedRestor}},
	}

	for _, tc := range restoreFailures {
		t.Run("복구의 "+tc.name+"까지 실패하면 세 오류를 함께 담아 복구하지 못했다고 알린다", func(t *testing.T) {
			t.Parallel()

			// given
			path, _ := hostsFixture(t, original)
			ops, _ := withRewriteOpens(defaultFileOps(), rewriteFaults{writeErr: errInjectedWrite}, tc.restore)
			ops, _ = withRenameFailure(ops)

			// when
			err := ops.writeHostsFile(path, shortContent, []byte(original))

			// then
			if err == nil {
				t.Fatal("expected error")
			}
			for _, want := range []error{errInjectedRename, errInjectedWrite, errInjectedRestor} {
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want wrapping %v", err, want)
				}
			}
			if !strings.Contains(err.Error(), "original content not restored") {
				t.Fatalf("err = %q", err)
			}
		})
	}

	t.Run("잘라내기 실패로 새 내용 뒤에 원본이 남아도 복구하면 원본과 같다", func(t *testing.T) {
		t.Parallel()

		// given
		path, _ := hostsFixture(t, original)
		ops, _ := withRewriteOpens(defaultFileOps(), rewriteFaults{truncateErr: errInjectedWrite})
		ops, _ = withRenameFailure(ops)

		// when
		err := ops.writeHostsFile(path, shortContent, []byte(original))

		// then
		if err == nil {
			t.Fatal("expected error")
		}
		if got := readFile(t, path); got != original {
			t.Fatalf("content = %q, want original", got)
		}
	})
}

func TestWriteHostsFilePaths(t *testing.T) {
	t.Parallel()

	t.Run("심볼릭 링크 경로는 대상 파일의 실제 경로로 rename한다", func(t *testing.T) {
		t.Parallel()

		// given
		dir := t.TempDir()
		target := filepath.Join(dir, "real-hosts")
		link := filepath.Join(dir, "hosts")
		writeFile(t, target, baseHosts, 0o644)
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		wantTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("eval symlinks: %v", err)
		}
		ops := defaultFileOps()
		var renamed string
		ops.rename = func(oldpath, newpath string) error {
			renamed = newpath
			return os.Rename(oldpath, newpath)
		}

		// when
		err = ops.writeHostsFile(link, shortContent, []byte(baseHosts))

		// then
		if err != nil {
			t.Fatalf("writeHostsFile error: %v", err)
		}
		if renamed != wantTarget {
			t.Fatalf("renamed to %q, want %q", renamed, wantTarget)
		}
		if got := readFile(t, target); got != shortContent {
			t.Fatalf("target content = %q", got)
		}
	})

	t.Run("심볼릭 링크 경로에서 rename이 실패해도 링크를 유지하고 대상 파일에 쓴다", func(t *testing.T) {
		t.Parallel()

		// given
		dir := t.TempDir()
		target := filepath.Join(dir, "real-hosts")
		link := filepath.Join(dir, "hosts")
		writeFile(t, target, baseHosts, 0o644)
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		ops, _ := withRenameFailure(defaultFileOps())

		// when
		err := ops.writeHostsFile(link, shortContent, []byte(baseHosts))

		// then
		if err != nil {
			t.Fatalf("writeHostsFile error: %v", err)
		}
		if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("symlink was replaced")
		}
		if got := readFile(t, target); got != shortContent {
			t.Fatalf("target content = %q", got)
		}
	})

	t.Run("경로를 해석할 수 없으면 오류를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		path := filepath.Join(t.TempDir(), "missing")

		// when
		err := defaultFileOps().writeHostsFile(path, shortContent, nil)

		// then
		if err == nil || !strings.Contains(err.Error(), "resolve hosts file path") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("임시 파일 준비가 실패하면 오류를 반환하고 원본을 바꾸지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		path, _ := hostsFixture(t, baseHosts)
		ops := withFaultyTemp(defaultFileOps(), func(f *faultyTemp) { f.writeErr = errInjectedStep })

		// when
		err := ops.writeHostsFile(path, shortContent, []byte(baseHosts))

		// then
		if !errors.Is(err, errInjectedStep) {
			t.Fatalf("err = %v", err)
		}
		if got := readFile(t, path); got != baseHosts {
			t.Fatalf("content = %q, want unchanged", got)
		}
	})
}

func TestDefaultFileOperations(t *testing.T) {
	t.Parallel()

	t.Run("존재하지 않는 디렉터리에서는 임시 파일 생성이 오류를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		dir := filepath.Join(t.TempDir(), "missing")

		// when
		tmp, err := defaultFileOps().createTemp(dir, tempFilePattern)

		// then
		if err == nil || tmp != nil {
			t.Fatalf("tmp = %v, err = %v", tmp, err)
		}
	})

	t.Run("존재하지 않는 파일은 제자리 쓰기용으로 열 수 없다", func(t *testing.T) {
		t.Parallel()

		// given
		path := filepath.Join(t.TempDir(), "missing")

		// when
		f, err := defaultFileOps().openForRewrite(path)

		// then
		if err == nil || f != nil {
			t.Fatalf("f = %v, err = %v", f, err)
		}
	})
}

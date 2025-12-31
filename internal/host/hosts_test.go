package host

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dockerBlock = "# Added by Docker Desktop\n" +
	"192.168.65.254 host.docker.internal\n" +
	"192.168.65.254 gateway.docker.internal\n" +
	"# To allow the same kube context to work on the host and the container:\n" +
	"127.0.0.1 kubernetes.docker.internal\n" +
	"# End of section\n"

const baseHosts = "127.0.0.1\tlocalhost\n255.255.255.255\tbroadcasthost\n::1 localhost\n"

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod fixture: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func newFixture(t *testing.T, content string) (*hostsFileAdapter, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts")
	writeFile(t, path, content, 0o644)
	return newHostsFileAdapterWithPath(path), path
}

func ourSection(entries ...string) string {
	var b strings.Builder
	b.WriteString(startMarker + "\n" + sectionNotice + "\n")
	for _, e := range entries {
		b.WriteString("127.0.0.1\t" + e + "\n")
	}
	b.WriteString(endMarker + "\n")
	return b.String()
}

func TestAddEntry(t *testing.T) {
	t.Run("구간이 없으면 새 종료 마커와 함께 구간을 만든다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts)

		// when
		err := adapter.AddEntry("web.svc.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		want := baseHosts + "\n" + ourSection("web.svc.local")
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("기존 레거시 구간 안에 항목을 추가한다", func(t *testing.T) {
		// given
		legacy := startMarker + "\n127.0.0.1\ta.local\n" + legacyEndMarker + "\n"
		adapter, path := newFixture(t, baseHosts+legacy)

		// when
		err := adapter.AddEntry("b.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		want := baseHosts + startMarker + "\n127.0.0.1\ta.local\n127.0.0.1\tb.local\n" + legacyEndMarker + "\n"
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("Docker Desktop 구간이 앞에 있어도 우리 구간 안에만 항목을 추가한다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts+dockerBlock+ourSection("a.local"))

		// when
		err := adapter.AddEntry("b.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		want := baseHosts + dockerBlock + ourSection("a.local", "b.local")
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("Docker Desktop 구간이 뒤에 있어도 우리 구간 안에만 항목을 추가한다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts+ourSection("a.local")+dockerBlock)

		// when
		err := adapter.AddEntry("b.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		want := baseHosts + ourSection("a.local", "b.local") + dockerBlock
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("구간 밖 Docker Desktop 종료 마커만 있으면 새 구간을 만들고 Docker 구간은 그대로 둔다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts+dockerBlock)

		// when
		err := adapter.AddEntry("a.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		want := baseHosts + dockerBlock + "\n" + ourSection("a.local")
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("종료 마커가 없는 구간이면 오류를 반환하고 파일을 바꾸지 않는다", func(t *testing.T) {
		// given
		malformed := baseHosts + startMarker + "\n127.0.0.1\ta.local\n10.0.0.1 keep.local\n"
		adapter, path := newFixture(t, malformed)

		// when
		err := adapter.AddEntry("b.local")

		// then
		if err == nil {
			t.Fatal("expected error for malformed section")
		}
		if got := readFile(t, path); got != malformed {
			t.Fatalf("file changed:\n%s", got)
		}
	})

	t.Run("구간 밖 줄에 이미 매핑된 호스트명이면 오류를 반환하고 파일을 바꾸지 않는다", func(t *testing.T) {
		// given
		original := baseHosts + "10.0.0.5\tfoo.local bar.local # note\n"
		adapter, path := newFixture(t, original)

		// when
		err := adapter.AddEntry("Bar.Local")

		// then
		if err == nil {
			t.Fatal("expected conflict error")
		}
		if got := readFile(t, path); got != original {
			t.Fatalf("file changed:\n%s", got)
		}
	})

	t.Run("구간 밖 주석에만 있는 호스트명은 충돌로 보지 않는다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts+"# 10.0.0.5 foo.local\n10.0.0.6 other.local # foo.local\n")

		// when
		err := adapter.AddEntry("foo.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		if got := readFile(t, path); !strings.Contains(got, "127.0.0.1\tfoo.local\n") {
			t.Fatalf("entry missing:\n%s", got)
		}
	})

	t.Run("대소문자만 다른 중복 항목은 쓰기 없이 무시한다", func(t *testing.T) {
		// given
		original := baseHosts + ourSection("web.local")
		adapter, path := newFixture(t, original)
		before, _ := os.Stat(path)

		// when
		err := adapter.AddEntry("WEB.Local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		after, _ := os.Stat(path)
		if got := readFile(t, path); got != original {
			t.Fatalf("file changed:\n%s", got)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Fatal("file was rewritten")
		}
	})

	t.Run("호스트명을 소문자로 기록한다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts)

		// when
		err := adapter.AddEntry("MiXed.Local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		if got := readFile(t, path); !strings.Contains(got, "127.0.0.1\tmixed.local\n") {
			t.Fatalf("lowercase entry missing:\n%s", got)
		}
	})

	t.Run("빈 호스트명이면 오류를 반환한다", func(t *testing.T) {
		// given
		adapter, _ := newFixture(t, baseHosts)

		// when
		err := adapter.AddEntry("  ")

		// then
		if err == nil {
			t.Fatal("expected error for empty hostname")
		}
	})
}

func TestRemoveEntry(t *testing.T) {
	t.Run("우리 구간의 항목만 지우고 Docker Desktop 구간은 그대로 둔다", func(t *testing.T) {
		// given
		before := baseHosts + dockerBlock + ourSection("a.local", "b.local")
		adapter, path := newFixture(t, before)

		// when
		err := adapter.RemoveEntry("A.local")

		// then
		if err != nil {
			t.Fatalf("RemoveEntry error: %v", err)
		}
		want := baseHosts + dockerBlock + ourSection("b.local")
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("구간 밖에 같은 이름이 있어도 건드리지 않는다", func(t *testing.T) {
		// given
		before := baseHosts + "10.0.0.5\ta.local\n" + ourSection("b.local") + dockerBlock
		adapter, path := newFixture(t, before)

		// when
		err := adapter.RemoveEntry("a.local")

		// then
		if err != nil {
			t.Fatalf("RemoveEntry error: %v", err)
		}
		if got := readFile(t, path); got != before {
			t.Fatalf("file changed:\n%s", got)
		}
	})

	t.Run("종료 마커가 없는 구간이면 오류를 반환하고 파일을 바꾸지 않는다", func(t *testing.T) {
		// given
		malformed := baseHosts + startMarker + "\n127.0.0.1\ta.local\n"
		adapter, path := newFixture(t, malformed)

		// when
		err := adapter.RemoveEntry("a.local")

		// then
		if err == nil {
			t.Fatal("expected error for malformed section")
		}
		if got := readFile(t, path); got != malformed {
			t.Fatalf("file changed:\n%s", got)
		}
	})
}

func TestClearAllEntries(t *testing.T) {
	t.Run("우리 구간만 지우고 앞뒤 Docker Desktop 구간은 그대로 둔다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts+dockerBlock+ourSection("a.local")+dockerBlock)

		// when
		err := adapter.ClearAllEntries()

		// then
		if err != nil {
			t.Fatalf("ClearAllEntries error: %v", err)
		}
		want := baseHosts + dockerBlock + dockerBlock
		if got := readFile(t, path); got != want {
			t.Fatalf("content mismatch\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("레거시 종료 마커 구간도 지운다", func(t *testing.T) {
		// given
		legacy := startMarker + "\n127.0.0.1\ta.local\n" + legacyEndMarker + "\n"
		adapter, path := newFixture(t, baseHosts+legacy)

		// when
		err := adapter.ClearAllEntries()

		// then
		if err != nil {
			t.Fatalf("ClearAllEntries error: %v", err)
		}
		if got := readFile(t, path); got != baseHosts {
			t.Fatalf("content mismatch:\n%s", got)
		}
	})

	t.Run("종료 마커가 없는 구간이면 오류를 반환하고 파일을 바꾸지 않는다", func(t *testing.T) {
		// given
		malformed := baseHosts + startMarker + "\n127.0.0.1\ta.local\n" + "10.0.0.1 keep.local\n"
		adapter, path := newFixture(t, malformed)

		// when
		err := adapter.ClearAllEntries()

		// then
		if err == nil {
			t.Fatal("expected error for malformed section")
		}
		if got := readFile(t, path); got != malformed {
			t.Fatalf("file changed:\n%s", got)
		}
	})

	t.Run("구간이 없으면 파일을 바꾸지 않는다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts+dockerBlock)

		// when
		err := adapter.ClearAllEntries()

		// then
		if err != nil {
			t.Fatalf("ClearAllEntries error: %v", err)
		}
		if got := readFile(t, path); got != baseHosts+dockerBlock {
			t.Fatalf("file changed:\n%s", got)
		}
	})
}

func TestMalformedSectionBeforeForeignSection(t *testing.T) {
	malformed := baseHosts + startMarker + "\n127.0.0.1\ta.local\n" + dockerBlock

	operations := map[string]func(*hostsFileAdapter) error{
		"AddEntry":        func(a *hostsFileAdapter) error { return a.AddEntry("b.local") },
		"RemoveEntry":     func(a *hostsFileAdapter) error { return a.RemoveEntry("a.local") },
		"ClearAllEntries": func(a *hostsFileAdapter) error { return a.ClearAllEntries() },
	}

	for name, operation := range operations {
		t.Run(name+"은 종료 마커 없이 Docker Desktop 구간이 이어지면 오류를 내고 파일을 바이트 단위로 그대로 둔다", func(t *testing.T) {
			// given
			adapter, path := newFixture(t, malformed)

			// when
			err := operation(adapter)

			// then
			if !errors.Is(err, errMalformedSection) {
				t.Fatalf("err = %v, want errMalformedSection", err)
			}
			if got := readFile(t, path); got != malformed {
				t.Fatalf("file changed:\n%s", got)
			}
		})
	}
}

func TestWriteHostsFile(t *testing.T) {
	t.Run("파일 권한을 유지한 채 내용을 갱신한다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "hosts")
		writeFile(t, path, baseHosts, 0o640)
		adapter := newHostsFileAdapterWithPath(path)

		// when
		err := adapter.AddEntry("a.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat: %v", statErr)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
		}
	})

	t.Run("심볼릭 링크 경로는 링크를 유지하고 대상 파일을 갱신한다", func(t *testing.T) {
		// given
		dir := t.TempDir()
		target := filepath.Join(dir, "real-hosts")
		link := filepath.Join(dir, "hosts")
		writeFile(t, target, baseHosts, 0o644)
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		adapter := newHostsFileAdapterWithPath(link)

		// when
		err := adapter.AddEntry("a.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		info, statErr := os.Lstat(link)
		if statErr != nil {
			t.Fatalf("lstat: %v", statErr)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("symlink was replaced by a regular file")
		}
		if got := readFile(t, target); !strings.Contains(got, "127.0.0.1\ta.local\n") {
			t.Fatalf("target not updated:\n%s", got)
		}
	})

	t.Run("성공 후 같은 디렉터리에 임시 파일을 남기지 않는다", func(t *testing.T) {
		// given
		adapter, path := newFixture(t, baseHosts)

		// when
		err := adapter.AddEntry("a.local")

		// then
		if err != nil {
			t.Fatalf("AddEntry error: %v", err)
		}
		entries, readErr := os.ReadDir(filepath.Dir(path))
		if readErr != nil {
			t.Fatalf("read dir: %v", readErr)
		}
		if len(entries) != 1 {
			t.Fatalf("directory has %d entries, want 1", len(entries))
		}
	})

	t.Run("hosts 파일이 없으면 오류를 반환한다", func(t *testing.T) {
		// given
		adapter := newHostsFileAdapterWithPath(filepath.Join(t.TempDir(), "missing"))

		// when
		err := adapter.AddEntry("a.local")

		// then
		if err == nil {
			t.Fatal("expected error for missing hosts file")
		}
	})
}

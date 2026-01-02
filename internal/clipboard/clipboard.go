package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	CopyTimeout  = 3 * time.Second
	pbcopyPath   = "/usr/bin/pbcopy"
	launchctl    = "/bin/launchctl"
	sudoPath     = "/usr/bin/sudo"
	darwinGOOS   = "darwin"
	rootUserName = "root"
)

// ErrUnsupported reports that no system clipboard command exists for this platform.
var ErrUnsupported = errors.New("system clipboard is not supported on this platform")

type Copier interface {
	Copy(ctx context.Context, text string) error
}

// Command is a program and its arguments, run without a shell.
type Command struct {
	Path string
	Args []string
}

type runner func(ctx context.Context, command Command, stdin string) error

// System copies text with pbcopy on macOS. Under sudo it runs pbcopy in the invoking user's session.
type System struct {
	goos    string
	command Command
	run     runner
	timeout time.Duration
}

func NewSystem() *System {
	return &System{
		goos:    runtime.GOOS,
		command: PasteboardCommand(os.Geteuid(), os.Getenv),
		run:     runCommand,
		timeout: CopyTimeout,
	}
}

func (s *System) Copy(ctx context.Context, text string) error {
	if s.goos != darwinGOOS {
		return ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.run(ctx, s.command, text)
}

// PasteboardCommand returns the pbcopy invocation for the given effective user ID and environment.
func PasteboardCommand(euid int, getenv func(string) string) Command {
	if euid != 0 {
		return Command{Path: pbcopyPath}
	}
	uid := getenv("SUDO_UID")
	user := getenv("SUDO_USER")
	if !isNumeric(uid) || user == "" || user == rootUserName {
		return Command{Path: pbcopyPath}
	}
	return Command{
		Path: launchctl,
		Args: []string{"asuser", uid, sudoPath, "-n", "-u", user, pbcopyPath},
	}
}

func isNumeric(value string) bool {
	_, err := strconv.ParseUint(value, 10, 32)
	return err == nil
}

func runCommand(ctx context.Context, command Command, stdin string) error {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Stdin = strings.NewReader(stdin)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("run %s: %w: %s", command.Path, err, detail)
	}
	return fmt.Errorf("run %s: %w", command.Path, err)
}

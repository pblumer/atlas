//go:build linux

package script

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxSandboxSystemAdapters(t *testing.T) {
	system := linuxSandboxSystem{}
	if ruleset, err := system.createRuleset(0); err == nil {
		if err := system.close(ruleset); err != nil {
			t.Fatalf("close empty ruleset: %v", err)
		}
	}

	dir := t.TempDir()
	fd, err := system.openPath(dir)
	if err != nil {
		t.Fatalf("openPath: %v", err)
	}
	if err := system.addPath(-1, fd, 0); err == nil {
		t.Error("addPath with an invalid ruleset unexpectedly succeeded")
	}
	if err := system.close(fd); err != nil {
		t.Fatalf("close path: %v", err)
	}
	if err := system.close(fd); err == nil {
		t.Error("closing an already closed descriptor unexpectedly succeeded")
	}
	if err := system.restrictSelf(-1); err == nil {
		t.Error("restrictSelf with an invalid ruleset unexpectedly succeeded")
	}

	// no_new_privs and an allow-only seccomp filter are irreversible for this
	// process but do not remove any capability the remaining tests need.
	if err := system.noNewPrivileges(); err != nil {
		t.Fatalf("noNewPrivileges: %v", err)
	}
	if err := system.installSeccomp([]unix.SockFilter{{Code: 0x06, K: unix.SECCOMP_RET_ALLOW}}); err != nil {
		t.Fatalf("install harmless seccomp filter: %v", err)
	}

	oldWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := system.chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	missing := filepath.Join(dir, "missing-interpreter")
	if err := system.exec(missing, []string{missing}, os.Environ()); err == nil {
		t.Error("exec of a missing interpreter unexpectedly succeeded")
	}
}

type recordingSandboxSystem struct {
	abi        int
	abiErr     error
	fail       string
	failPath   string
	nextFD     int
	paths      map[int]string
	allowed    map[string]uint64
	handled    uint64
	noNewPrivs bool
	restricted bool
	filter     []unix.SockFilter
	workingDir string
	execPath   string
	execArgv   []string
	execEnv    []string
}

func newRecordingSandboxSystem() *recordingSandboxSystem {
	return &recordingSandboxSystem{abi: 5, nextFD: 10, paths: map[int]string{}, allowed: map[string]uint64{}}
}

func (s *recordingSandboxSystem) landlockABI() (int, error) { return s.abi, s.abiErr }
func (s *recordingSandboxSystem) createRuleset(handled uint64) (int, error) {
	if s.fail == "create" {
		return 0, errors.New("create refused")
	}
	s.handled = handled
	return 9, nil
}
func (s *recordingSandboxSystem) openPath(path string) (int, error) {
	if s.fail == "open" && path == "/usr" {
		return 0, errors.New("open refused")
	}
	if s.fail == "missing" && path == "/usr" {
		return 0, unix.ENOENT
	}
	s.nextFD++
	s.paths[s.nextFD] = path
	return s.nextFD, nil
}
func (s *recordingSandboxSystem) addPath(_ int, parent int, access uint64) error {
	if s.fail == "add" && (s.failPath == "" && s.paths[parent] == "/usr" || s.paths[parent] == s.failPath) {
		return errors.New("add refused")
	}
	s.allowed[s.paths[parent]] = access
	return nil
}

func TestStrictSandboxFailsClosedForEveryAllowlistClass(t *testing.T) {
	for _, path := range []string{"/etc/alternatives", "/etc/ld.so.cache", "/dev/null", "/tmp/atlas-script"} {
		t.Run(path, func(t *testing.T) {
			system := newRecordingSandboxSystem()
			system.fail = "add"
			system.failPath = path
			err := installFilesystemPolicyWith(system, "/tmp/atlas-script")
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("allowlist error = %v, want path %q", err, path)
			}
		})
	}
}
func (s *recordingSandboxSystem) close(int) error { return nil }
func (s *recordingSandboxSystem) noNewPrivileges() error {
	if s.fail == "no-new-privs" {
		return errors.New("prctl refused")
	}
	s.noNewPrivs = true
	return nil
}
func (s *recordingSandboxSystem) restrictSelf(int) error {
	if s.fail == "restrict" {
		return errors.New("restrict refused")
	}
	s.restricted = true
	return nil
}
func (s *recordingSandboxSystem) installSeccomp(filter []unix.SockFilter) error {
	if s.fail == "seccomp" {
		return errors.New("seccomp refused")
	}
	s.filter = append([]unix.SockFilter(nil), filter...)
	return nil
}
func (s *recordingSandboxSystem) chdir(path string) error {
	if s.fail == "chdir" {
		return errors.New("chdir refused")
	}
	s.workingDir = path
	return nil
}
func (s *recordingSandboxSystem) exec(path string, argv, env []string) error {
	if s.fail == "exec" {
		return errors.New("exec refused")
	}
	s.execPath = path
	s.execArgv = append([]string(nil), argv...)
	s.execEnv = append([]string(nil), env...)
	return nil
}

func TestStrictSandboxPolicyIsCompleteBeforeExec(t *testing.T) {
	system := newRecordingSandboxSystem()
	err := runSandboxWith(system, "/tmp/atlas-script-one", []string{"/usr/bin/python3", "-c", "pass"}, []string{"HOME=/tmp/atlas-script-one"})
	if err != nil {
		t.Fatalf("runSandboxWith: %v", err)
	}
	if !system.noNewPrivs || !system.restricted || len(system.filter) == 0 {
		t.Fatalf("policy incomplete: no_new_privs=%v restricted=%v filter=%d", system.noNewPrivs, system.restricted, len(system.filter))
	}
	if system.workingDir != "/tmp/atlas-script-one" || system.execPath != "/usr/bin/python3" {
		t.Errorf("launch = cwd %q exec %q", system.workingDir, system.execPath)
	}
	if strings.Join(system.execArgv, " ") != "/usr/bin/python3 -c pass" || strings.Join(system.execEnv, " ") != "HOME=/tmp/atlas-script-one" {
		t.Errorf("exec argv/env changed: %q %q", system.execArgv, system.execEnv)
	}
	if got := system.allowed["/usr"]; got&unix.LANDLOCK_ACCESS_FS_READ_FILE == 0 || got&unix.LANDLOCK_ACCESS_FS_EXECUTE == 0 || got&unix.LANDLOCK_ACCESS_FS_WRITE_FILE != 0 {
		t.Errorf("/usr access = %#x, want read/execute without write", got)
	}
	scratch := system.allowed["/tmp/atlas-script-one"]
	for _, right := range []uint64{unix.LANDLOCK_ACCESS_FS_READ_FILE, unix.LANDLOCK_ACCESS_FS_WRITE_FILE, unix.LANDLOCK_ACCESS_FS_REFER, unix.LANDLOCK_ACCESS_FS_TRUNCATE} {
		if scratch&right == 0 {
			t.Errorf("scratch access %#x is missing right %#x", scratch, right)
		}
	}
	if scratch&unix.LANDLOCK_ACCESS_FS_EXECUTE != 0 {
		t.Errorf("scratch access %#x permits executing a file written by the script", scratch)
	}
	if system.handled&unix.LANDLOCK_ACCESS_FS_IOCTL_DEV == 0 {
		t.Error("Landlock ABI 5 policy does not handle device ioctls")
	}
	if system.allowed["/dev/null"]&unix.LANDLOCK_ACCESS_FS_WRITE_FILE == 0 {
		t.Error("/dev/null is not writable")
	}
	for _, path := range []string{"/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/localtime"} {
		if got := system.allowed[path]; got&unix.LANDLOCK_ACCESS_FS_READ_FILE == 0 || got&unix.LANDLOCK_ACCESS_FS_READ_DIR != 0 {
			t.Errorf("%s access = %#x, want file read without directory read", path, got)
		}
	}
}

func TestStrictSandboxPolicyFailsClosedAtEveryStage(t *testing.T) {
	tests := []struct {
		stage string
		want  string
	}{
		{"create", "create Landlock"},
		{"open", "open allowed path"},
		{"add", "allow path"},
		{"no-new-privs", "no_new_privs"},
		{"restrict", "enforce Landlock"},
		{"seccomp", "seccomp"},
		{"chdir", "enter scratch"},
		{"exec", "exec interpreter"},
	}
	for _, tc := range tests {
		t.Run(tc.stage, func(t *testing.T) {
			system := newRecordingSandboxSystem()
			system.fail = tc.stage
			err := runSandboxWith(system, "/tmp/atlas-script", []string{"/usr/bin/python3"}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if system.execPath != "" {
				t.Errorf("interpreter executed after %s failure", tc.stage)
			}
		})
	}
	if err := runSandboxWith(newRecordingSandboxSystem(), "/tmp/x", nil, nil); err == nil {
		t.Error("missing interpreter was accepted")
	}
}

func TestStrictSandboxSupportAndMissingOptionalPaths(t *testing.T) {
	system := newRecordingSandboxSystem()
	if err := sandboxSupportWith(system); err != nil {
		t.Fatalf("supported ABI: %v", err)
	}
	system.abi = 2
	if err := sandboxSupportWith(system); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("old ABI error = %v", err)
	}
	system.abiErr = errors.New("blocked")
	if err := sandboxSupportWith(system); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("blocked ABI error = %v", err)
	}
	if err := installFilesystemPolicyWith(system, "/tmp/atlas-script"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("blocked policy ABI error = %v", err)
	}
	tooOld := newRecordingSandboxSystem()
	tooOld.abi = 2
	if err := installFilesystemPolicyWith(tooOld, "/tmp/atlas-script"); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("old policy ABI error = %v", err)
	}

	missing := newRecordingSandboxSystem()
	missing.fail = "missing"
	if err := installFilesystemPolicyWith(missing, "/tmp/atlas-script"); err != nil {
		t.Fatalf("missing optional runtime path: %v", err)
	}
	if _, added := missing.allowed["/usr"]; added {
		t.Error("missing path was added to the ruleset")
	}
	if got := handledFilesystemAccess(3); got&unix.LANDLOCK_ACCESS_FS_IOCTL_DEV != 0 {
		t.Errorf("ABI 3 handled unsupported ioctl right: %#x", got)
	}
	if err := errnoErr(0); err != nil {
		t.Errorf("zero errno = %v", err)
	}
	if err := errnoErr(unix.EPERM); !errors.Is(err, unix.EPERM) {
		t.Errorf("EPERM errno = %v", err)
	}
}

func TestNoSocketFilterCoversSocketPairAndIOUring(t *testing.T) {
	arch, err := auditArchitecture()
	if err != nil {
		t.Fatal(err)
	}
	filter := noSocketFilter(arch)
	for _, syscall := range []uint32{uint32(unix.SYS_SOCKET), uint32(unix.SYS_SOCKETPAIR), uint32(unix.SYS_IO_URING_SETUP)} {
		if got := evaluateSandboxFilter(t, filter, arch, syscall); got != uint32(unix.SECCOMP_RET_ERRNO)|uint32(unix.EPERM) {
			t.Errorf("syscall %d action = %#x, want EPERM", syscall, got)
		}
	}
	if got := evaluateSandboxFilter(t, filter, arch, uint32(unix.SYS_READ)); got != unix.SECCOMP_RET_ALLOW {
		t.Errorf("read action = %#x, want allow", got)
	}
	if got := evaluateSandboxFilter(t, filter, arch+1, uint32(unix.SYS_READ)); got != unix.SECCOMP_RET_KILL_PROCESS {
		t.Errorf("wrong-architecture action = %#x, want kill", got)
	}
	if arch == unix.AUDIT_ARCH_X86_64 {
		const x32SyscallBit = 0x40000000
		if got := evaluateSandboxFilter(t, filter, arch, uint32(unix.SYS_READ)|x32SyscallBit); got != unix.SECCOMP_RET_KILL_PROCESS {
			t.Errorf("x32 syscall action = %#x, want kill", got)
		}
	}
}

// evaluateSandboxFilter interprets the small classic-BPF instruction subset the
// sandbox policy uses. Keeping the assertion at the action level catches a jump
// offset that merely listing the syscall constants would miss.
func evaluateSandboxFilter(t *testing.T, filter []unix.SockFilter, arch, syscall uint32) uint32 {
	t.Helper()
	var accumulator uint32
	for pc := 0; pc < len(filter); {
		instruction := filter[pc]
		switch instruction.Code {
		case 0x20: // BPF_LD | BPF_W | BPF_ABS
			switch instruction.K {
			case 0:
				accumulator = syscall
			case 4:
				accumulator = arch
			default:
				t.Fatalf("unexpected seccomp_data offset %d", instruction.K)
			}
			pc++
		case 0x15: // BPF_JMP | BPF_JEQ | BPF_K
			if accumulator == instruction.K {
				pc += int(instruction.Jt) + 1
			} else {
				pc += int(instruction.Jf) + 1
			}
		case 0x45: // BPF_JMP | BPF_JSET | BPF_K
			if accumulator&instruction.K != 0 {
				pc += int(instruction.Jt) + 1
			} else {
				pc += int(instruction.Jf) + 1
			}
		case 0x06: // BPF_RET | BPF_K
			return instruction.K
		default:
			t.Fatalf("unexpected BPF instruction %#x", instruction.Code)
		}
	}
	t.Fatal("filter terminated without an action")
	return 0
}

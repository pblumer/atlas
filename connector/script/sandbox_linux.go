//go:build linux

package script

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const minimumLandlockABI = 3

type landlockRulesetAttr struct {
	HandledAccessFS  uint64
	HandledAccessNet uint64
	Scoped           uint64
}

type landlockPathBeneathAttr struct {
	AllowedAccess uint64
	ParentFD      int32
	_             uint32
}

// sandboxSystem keeps the policy separate from its irreversible Linux syscalls.
// Tests drive the same policy against a recorder even on CI hosts whose outer
// seccomp profile blocks Landlock.
type sandboxSystem interface {
	landlockABI() (int, error)
	createRuleset(handled uint64) (int, error)
	openPath(path string) (int, error)
	addPath(ruleset, parent int, access uint64) error
	close(fd int) error
	noNewPrivileges() error
	restrictSelf(ruleset int) error
	installSeccomp(filter []unix.SockFilter) error
	chdir(path string) error
	exec(path string, argv, env []string) error
}

type linuxSandboxSystem struct{}

func (linuxSandboxSystem) landlockABI() (int, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(abi), nil
}

func (linuxSandboxSystem) createRuleset(handled uint64) (int, error) {
	attr := landlockRulesetAttr{HandledAccessFS: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	return int(fd), errnoErr(errno)
}

func (linuxSandboxSystem) openPath(path string) (int, error) {
	return unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
}

func (linuxSandboxSystem) addPath(ruleset, parent int, access uint64) error {
	attr := landlockPathBeneathAttr{AllowedAccess: access, ParentFD: int32(parent)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	return errnoErr(errno)
}

func (linuxSandboxSystem) close(fd int) error { return unix.Close(fd) }
func (linuxSandboxSystem) noNewPrivileges() error {
	return unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
}
func (linuxSandboxSystem) restrictSelf(ruleset int) error {
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0)
	return errnoErr(errno)
}
func (linuxSandboxSystem) installSeccomp(filter []unix.SockFilter) error {
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	return unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0)
}
func (linuxSandboxSystem) chdir(path string) error { return os.Chdir(path) }
func (linuxSandboxSystem) exec(path string, argv, env []string) error {
	return unix.Exec(path, argv, env)
}

func errnoErr(errno unix.Errno) error {
	if errno == 0 {
		return nil
	}
	return errno
}

func sandboxSupport() error { return sandboxSupportWith(linuxSandboxSystem{}) }

func sandboxSupportWith(system sandboxSystem) error {
	abi, err := system.landlockABI()
	if err != nil {
		return fmt.Errorf("script sandbox: Landlock is unavailable: %w", err)
	}
	if abi < minimumLandlockABI {
		return fmt.Errorf("script sandbox: Landlock ABI %d is too old (need %d or newer)", abi, minimumLandlockABI)
	}
	if _, err := auditArchitecture(); err != nil {
		return err
	}
	return nil
}

func runSandbox(scratch string, argv, env []string) error {
	// Landlock and seccomp apply to a thread and its descendants. Pinning this
	// goroutine guarantees the same restricted thread performs exec; exec then
	// removes every other Go runtime thread before interpreted code can run.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return runSandboxWith(linuxSandboxSystem{}, scratch, argv, env)
}

func runSandboxWith(system sandboxSystem, scratch string, argv, env []string) error {
	if len(argv) == 0 {
		return errors.New("script sandbox: missing interpreter")
	}
	if err := installFilesystemPolicyWith(system, scratch); err != nil {
		return err
	}
	if err := installNoSocketPolicyWith(system); err != nil {
		return err
	}
	if err := system.chdir(scratch); err != nil {
		return fmt.Errorf("script sandbox: enter scratch: %w", err)
	}
	if err := system.exec(argv[0], argv, env); err != nil {
		return fmt.Errorf("script sandbox: exec interpreter: %w", err)
	}
	return nil
}

func installFilesystemPolicyWith(system sandboxSystem, scratch string) error {
	abi, err := system.landlockABI()
	if err != nil {
		return fmt.Errorf("script sandbox: Landlock is unavailable: %w", err)
	}
	if abi < minimumLandlockABI {
		return fmt.Errorf("script sandbox: Landlock ABI %d is too old (need %d or newer)", abi, minimumLandlockABI)
	}
	handled := handledFilesystemAccess(abi)
	ruleset, err := system.createRuleset(handled)
	if err != nil {
		return fmt.Errorf("script sandbox: create Landlock ruleset: %w", err)
	}
	defer system.close(ruleset)

	readExec := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	for _, path := range sandboxRuntimeRoots() {
		if err := addAllowedPath(system, ruleset, path, readExec); err != nil {
			return err
		}
	}
	readOnlyDirectory := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	for _, path := range []string{
		"/etc/alternatives", "/etc/ld.so.conf.d", "/etc/ssl/certs", "/etc/pki",
	} {
		if err := addAllowedPath(system, ruleset, path, readOnlyDirectory); err != nil {
			return err
		}
	}
	readOnlyFile := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE)
	for _, path := range []string{"/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/localtime"} {
		if err := addAllowedPath(system, ruleset, path, readOnlyFile); err != nil {
			return err
		}
	}
	deviceAccess := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	for _, path := range []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"} {
		if err := addAllowedPath(system, ruleset, path, deviceAccess); err != nil {
			return err
		}
	}
	// Scratch deliberately omits execute and special-file creation. REFER remains
	// constrained by Landlock's requirement that both source and target grant it,
	// so files outside the allowlist cannot be linked into scratch.
	scratchAccess := uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
		unix.LANDLOCK_ACCESS_FS_REFER |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE)
	if err := addAllowedPath(system, ruleset, scratch, scratchAccess); err != nil {
		return err
	}
	if err := system.noNewPrivileges(); err != nil {
		return fmt.Errorf("script sandbox: set no_new_privs: %w", err)
	}
	if err := system.restrictSelf(ruleset); err != nil {
		return fmt.Errorf("script sandbox: enforce Landlock ruleset: %w", err)
	}
	return nil
}

func handledFilesystemAccess(abi int) uint64 {
	handled := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
		unix.LANDLOCK_ACCESS_FS_REFER |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE)
	if abi >= 5 {
		handled |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return handled
}

func sandboxRuntimeRoots() []string {
	return []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/usr/local", "/opt/microsoft/powershell"}
}

func addAllowedPath(system sandboxSystem, ruleset int, path string, access uint64) error {
	fd, err := system.openPath(path)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("script sandbox: open allowed path %q: %w", path, err)
	}
	defer system.close(fd)
	if err := system.addPath(ruleset, fd, access); err != nil {
		return fmt.Errorf("script sandbox: allow path %q: %w", path, err)
	}
	return nil
}

func installNoSocketPolicyWith(system sandboxSystem) error {
	arch, err := auditArchitecture()
	if err != nil {
		return err
	}
	if err := system.installSeccomp(noSocketFilter(arch)); err != nil {
		return fmt.Errorf("script sandbox: install seccomp socket policy: %w", err)
	}
	return nil
}

func noSocketFilter(arch uint32) []unix.SockFilter {
	const (
		bpfLoadWordAbsolute = 0x20
		bpfJumpEqual        = 0x15
		bpfJumpBitsSet      = 0x45
		bpfReturn           = 0x06
		seccompDataNR       = 0
		seccompDataArch     = 4
		x32SyscallBit       = 0x40000000
	)
	deny := uint32(unix.SECCOMP_RET_ERRNO) | uint32(unix.EPERM)
	filter := []unix.SockFilter{
		{Code: bpfLoadWordAbsolute, K: seccompDataArch},
		{Code: bpfJumpEqual, Jt: 1, Jf: 0, K: arch},
		{Code: bpfReturn, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: bpfLoadWordAbsolute, K: seccompDataNR},
	}
	if arch == unix.AUDIT_ARCH_X86_64 {
		// x32 shares AUDIT_ARCH_X86_64 but adds a bit to every syscall number.
		// Without this guard, comparing only native numbers would miss its socket.
		filter = append(filter,
			unix.SockFilter{Code: bpfJumpBitsSet, Jt: 0, Jf: 1, K: x32SyscallBit},
			unix.SockFilter{Code: bpfReturn, K: unix.SECCOMP_RET_KILL_PROCESS})
	}
	return append(filter,
		unix.SockFilter{Code: bpfJumpEqual, Jt: 0, Jf: 1, K: uint32(unix.SYS_SOCKET)},
		unix.SockFilter{Code: bpfReturn, K: deny},
		unix.SockFilter{Code: bpfJumpEqual, Jt: 0, Jf: 1, K: uint32(unix.SYS_SOCKETPAIR)},
		unix.SockFilter{Code: bpfReturn, K: deny},
		// io_uring can create a socket without issuing SYS_SOCKET. No ring is
		// inherited by the launcher, so refusing setup closes that alternate path.
		unix.SockFilter{Code: bpfJumpEqual, Jt: 0, Jf: 1, K: uint32(unix.SYS_IO_URING_SETUP)},
		unix.SockFilter{Code: bpfReturn, K: deny},
		unix.SockFilter{Code: bpfReturn, K: unix.SECCOMP_RET_ALLOW},
	)
}

func auditArchitecture() (uint32, error) {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	case "arm":
		return unix.AUDIT_ARCH_ARM, nil
	default:
		return 0, fmt.Errorf("script sandbox: Linux architecture %s is unsupported", runtime.GOARCH)
	}
}

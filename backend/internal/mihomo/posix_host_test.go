package mihomo

import (
	"runtime"
	"testing"
)

// posixKernelHost reports whether this host can execute the mock mihomo kernel
// that the tests install at <data-dir>/mihomo. That placeholder is a POSIX shell
// script (or a `#!/bin/sh` wrapper that execs the test binary), and the manager
// always launches it through exec.Command(<data-dir>/mihomo, ...). Windows cannot
// execute an extensionless script (CreateProcess appends `.exe`), so these
// host-process tests are limited to POSIX hosts. The deployment target is Linux
// and the full, unchanged assertions are executed there.
const posixKernelHost = runtime.GOOS != "windows"

// posixFileModes reports whether this host enforces POSIX permission bits.
// Windows ignores the mode given to open/create (a file created with 0600
// reports 0666), so 0600/0700 assertions run only where the kernel enforces
// them. The Linux deployment target enforces them and runs these assertions.
const posixFileModes = runtime.GOOS != "windows"

// requirePOSIXKernelHost skips a mock-kernel test on a host that cannot run the
// POSIX shell script kernel. The test is not skipped on Linux, where every
// original assertion still runs.
func requirePOSIXKernelHost(t *testing.T) {
	t.Helper()
	if !posixKernelHost {
		t.Skip("mock mihomo kernel is a POSIX shell script at <data-dir>/mihomo; Windows cannot execute an extensionless script (assertions run on Linux)")
	}
}

// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package hostinfo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagernet/tailscale/types/ptr"
	"github.com/sagernet/tailscale/util/winutil"
	"github.com/sagernet/tailscale/util/winutil/winenv"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func init() {
	distroName = lazyDistroName.Get
	osVersion = lazyOSVersion.Get
	packageType = lazyPackageType.Get
}

var (
	lazyDistroName  = &lazyAtomicValue[string]{f: ptr.To(distroNameWindows)}
	lazyOSVersion   = &lazyAtomicValue[string]{f: ptr.To(osVersionWindows)}
	lazyPackageType = &lazyAtomicValue[string]{f: ptr.To(packageTypeWindows)}
)

func distroNameWindows() string {
	if winenv.IsWindowsServer() {
		return "Server"
	}
	return ""
}

func osVersionWindows() string {
	major, minor, build := windows.RtlGetNtVersionNumbers()
	s := fmt.Sprintf("%d.%d.%d", major, minor, build)
	// Windows 11 still uses 10 as its major number internally
	if major == 10 {
		if ubr, err := getUBR(); err == nil {
			s += fmt.Sprintf(".%d", ubr)
		}
	}
	return s // "10.0.19041.388", ideally
}

// getUBR obtains a fourth version field, the "Update Build Revision",
// from the registry. This field is only available beginning with Windows 10.
func getUBR() (uint32, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return 0, err
	}
	defer key.Close()

	val, valType, err := key.GetIntegerValue("UBR")
	if err != nil {
		return 0, err
	}
	if valType != registry.DWORD {
		return 0, registry.ErrUnexpectedType
	}

	return uint32(val), nil
}

func packageTypeWindows() string {
	if _, err := os.Stat(`C:\ProgramData\chocolatey\lib\tailscale`); err == nil {
		return "choco"
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(exe, filepath.Join(home, "scoop", "apps", "tailscale")) {
		return "scoop"
	}
	msiSentinel, _ := winutil.GetRegInteger("MSI")
	if msiSentinel != 1 {
		// Atypical. Not worth trying to detect. Likely open
		// source tailscaled or a developer running by hand.
		return ""
	}
	result := "msi"
	if env, _ := winutil.GetRegString("MSIDist"); env != "" {
		result += "/" + env
	}
	return result
}

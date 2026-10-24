//go:build windows

package main

// Windows' own "Open" and "Choose folder" dialogs (the modern Explorer-style
// IFileOpenDialog), called directly. The launcher starts no other program for
// them.

import (
	"errors"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	ole32                           = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	procSetWindowPos                = user32.NewProc("SetWindowPos")
)

type guid struct {
	d1     uint32
	d2, d3 uint16
	d4     [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidIShellItem       = guid{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	fosNoChangeDir     = 0x8
	fosPickFolders     = 0x20
	fosForceFileSystem = 0x40
	fosAllowMulti      = 0x200
	fosPathMustExist   = 0x800
	fosFileMustExist   = 0x1000
	sigdnFileSysPath   = 0x80058000
	hrCancelled        = 0x800704C7
)

// vtable slots
const (
	vRelease      = 2
	vShow         = 3
	vSetFileTypes = 4
	vSetOptions   = 9
	vGetOptions   = 10
	vSetFolder    = 12
	vSetTitle     = 17
	vGetResult    = 20
	vGetResults   = 27
	siGetName     = 5 // IShellItem.GetDisplayName
	saGetCount    = 7 // IShellItemArray.GetCount
	saGetItemAt   = 8
)

// vcall calls method idx of a COM object. Pointers passed in args (converted to
// uintptr) are moved to the heap and kept alive for the call, like
// syscall.LazyProc.Call does, so Windows never writes to a moved Go stack.
//
//go:uintptrescapes
func vcall(obj unsafe.Pointer, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		vcall(obj, vRelease)
	}
}

func itemPath(item unsafe.Pointer) string {
	var p *uint16
	if vcall(item, siGetName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))) != 0 || p == nil {
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p)
}

func utf16PtrToString(p *uint16) string {
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}

type filterSpec struct{ name, spec *uint16 }

// fileDialog shows the dialog and returns the chosen paths (none when cancelled).
func fileDialog(title, start string, folders, multi bool) ([]string, error) {
	type result struct {
		paths []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		// A thread of its own for the dialog. It is never unlocked, so Go ends the
		// thread with this goroutine and its COM state goes with it.
		runtime.LockOSThread()
		paths, err := showFileDialog(title, start, folders, multi)
		done <- result{paths, err}
	}()
	r := <-done
	return r.paths, r.err
}

func showFileDialog(title, start string, folders, multi bool) ([]string, error) {
	const coinitApartment, coinitNoOLE1DDE = 0x2, 0x4
	if hr, _, _ := procCoInitializeEx.Call(0, coinitApartment|coinitNoOLE1DDE); int32(hr) < 0 {
		return nil, errors.New("Windows could not open its file dialog")
	}
	defer procCoUninitialize.Call()

	var dlg unsafe.Pointer
	const clsctxInproc = 0x1
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInproc,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)), uintptr(unsafe.Pointer(&dlg))); hr != 0 || dlg == nil {
		return nil, errors.New("Windows could not open its file dialog")
	}
	defer release(dlg)

	var opts uint32
	vcall(dlg, vGetOptions, uintptr(unsafe.Pointer(&opts)))
	opts |= fosForceFileSystem | fosNoChangeDir | fosPathMustExist
	if folders {
		opts |= fosPickFolders
	} else {
		opts |= fosFileMustExist
	}
	if multi {
		opts |= fosAllowMulti
	}
	vcall(dlg, vSetOptions, uintptr(opts))

	t, _ := syscall.UTF16PtrFromString(title)
	vcall(dlg, vSetTitle, uintptr(unsafe.Pointer(t)))

	if !folders {
		n1, _ := syscall.UTF16PtrFromString("Mods (.zip, .utoc, .ucas, .pak)")
		s1, _ := syscall.UTF16PtrFromString("*.zip;*.utoc;*.ucas;*.pak")
		n2, _ := syscall.UTF16PtrFromString("All files")
		s2, _ := syscall.UTF16PtrFromString("*.*")
		specs := []filterSpec{{n1, s1}, {n2, s2}}
		vcall(dlg, vSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0])))
		runtime.KeepAlive(specs)
	}
	if start != "" && isDir(start) {
		sp, _ := syscall.UTF16PtrFromString(start)
		var folder unsafe.Pointer
		if hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(sp)), 0,
			uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&folder))); hr == 0 && folder != nil {
			vcall(dlg, vSetFolder, uintptr(folder))
			release(folder)
		}
	}

	// The dialog opens behind the launcher's window (Windows only lets the program
	// you clicked in come to the front), so bring it forward once it is up.
	stop := make(chan struct{})
	go raiseOwnDialog(stop)
	hr := vcall(dlg, vShow, 0)
	close(stop)
	if uint32(hr) == hrCancelled {
		return nil, nil
	}
	if hr != 0 {
		return nil, errors.New("Windows' file dialog did not open")
	}

	if !multi {
		var item unsafe.Pointer
		if vcall(dlg, vGetResult, uintptr(unsafe.Pointer(&item))) != 0 || item == nil {
			return nil, nil
		}
		defer release(item)
		if p := itemPath(item); p != "" {
			return []string{p}, nil
		}
		return nil, nil
	}
	var arr unsafe.Pointer
	if vcall(dlg, vGetResults, uintptr(unsafe.Pointer(&arr))) != 0 || arr == nil {
		return nil, nil
	}
	defer release(arr)
	var n uint32
	vcall(arr, saGetCount, uintptr(unsafe.Pointer(&n)))
	var out []string
	for i := uint32(0); i < n; i++ {
		var item unsafe.Pointer
		if vcall(arr, saGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))) == 0 && item != nil {
			if p := itemPath(item); p != "" {
				out = append(out, p)
			}
			release(item)
		}
	}
	return out, nil
}

// raiseOwnDialog waits for this program's dialog window and puts it in front.
func raiseOwnDialog(stop chan struct{}) {
	pid := uint32(syscall.Getpid())
	for i := 0; i < 40; i++ {
		select {
		case <-stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
		if h := ownDialogWindow(pid); h != 0 {
			const swpNoSize, swpNoMove, swpShow = 0x1, 0x2, 0x40
			hwndTopmost := ^uintptr(0) // HWND_TOPMOST (-1)
			procSetWindowPos.Call(h, hwndTopmost, 0, 0, 0, 0, swpNoSize|swpNoMove|swpShow)
			bringToFront(h)
			return
		}
	}
}

var (
	dlgMu       sync.Mutex
	dlgPid      uint32
	dlgFound    uintptr
	enumDialogs = syscall.NewCallback(func(h, _ uintptr) uintptr {
		var cls [16]uint16
		n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls)))
		if n == 0 || syscall.UTF16ToString(cls[:n]) != "#32770" {
			return 1
		}
		if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == dlgPid {
			dlgFound = h
			return 0
		}
		return 1
	})
)

func ownDialogWindow(pid uint32) uintptr {
	dlgMu.Lock()
	defer dlgMu.Unlock()
	dlgPid, dlgFound = pid, 0
	procEnumWindows.Call(enumDialogs, 0)
	return dlgFound
}

func pickFolder(title, start string, allowNew bool) (string, error) {
	paths, err := fileDialog(title, start, true, false)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func pickFiles(title string) ([]string, error) {
	return fileDialog(title, "", false, true)
}

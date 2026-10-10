//go:build windows

package bridge

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Query the same provider as Get-CimInstance, in-process, without a shell or
// extra dependencies. Toolhelp alone cannot supply the command line required
// to distinguish devices, main displays and application windows.
const scrcpyInventoryQuery = "SELECT ProcessId, ParentProcessId, CommandLine, CreationDate FROM Win32_Process WHERE Name = 'scrcpy.exe'"
const scrcpyInventoryTimeout = 6 * time.Second

var nativeScrcpyInventory = &processInventory{timeout: scrcpyInventoryTimeout, query: queryScrcpyWMI}

func listScrcpyProcs() ([]scrcpyProc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scrcpyInventoryTimeout)
	defer cancel()
	return nativeScrcpyInventory.load(ctx)
}

var (
	inventoryOle32                 = windows.NewLazySystemDLL("ole32.dll")
	inventoryOleaut32              = windows.NewLazySystemDLL("oleaut32.dll")
	inventoryCoInitializeEx        = inventoryOle32.NewProc("CoInitializeEx")
	inventoryCoUninitialize        = inventoryOle32.NewProc("CoUninitialize")
	inventoryCoInitializeSecurity  = inventoryOle32.NewProc("CoInitializeSecurity")
	inventoryCoCreateInstance      = inventoryOle32.NewProc("CoCreateInstance")
	inventoryCoSetProxyBlanket     = inventoryOle32.NewProc("CoSetProxyBlanket")
	inventoryCoEnableCancellation  = inventoryOle32.NewProc("CoEnableCallCancellation")
	inventoryCoDisableCancellation = inventoryOle32.NewProc("CoDisableCallCancellation")
	inventoryCoCancelCall          = inventoryOle32.NewProc("CoCancelCall")
	inventorySysAllocStringLen     = inventoryOleaut32.NewProc("SysAllocStringLen")
	inventorySysFreeString         = inventoryOleaut32.NewProc("SysFreeString")
	inventorySysStringLen          = inventoryOleaut32.NewProc("SysStringLen")
	inventoryVariantClear          = inventoryOleaut32.NewProc("VariantClear")
	inventorySecurityOnce          sync.Once
	inventorySecurityErr           error
	wbemLocatorCLSID               = windows.GUID{Data1: 0x4590f811, Data2: 0x1d3a, Data3: 0x11d0, Data4: [8]byte{0x89, 0x1f, 0, 0xaa, 0, 0x4b, 0x2e, 0x24}}
	wbemLocatorIID                 = windows.GUID{Data1: 0xdc12a687, Data2: 0x737f, Data3: 0x11cf, Data4: [8]byte{0x88, 0x4d, 0, 0xaa, 0, 0x4b, 0x2e, 0x24}}
)

// Layouts and method slots are from the Windows SDK's wbemcli.h/oaidl.h.
// The VARIANT union includes two pointers (VT_RECORD), so its total size is
// 24 bytes on 64-bit Windows and 16 bytes on 32-bit Windows.
type inventoryCOM struct{ vtable *[27]uintptr }
type inventoryVariant struct {
	kind     uint16
	reserved [3]uint16
	data     [2]uintptr
}

func inventoryHRESULT(operation string, value uintptr) error {
	if int32(uint32(value)) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08X", operation, uint32(value))
	}
	return nil
}

func (o *inventoryCOM) release() {
	if o != nil {
		syscall.SyscallN(o.vtable[2], uintptr(unsafe.Pointer(o)))
	}
}

func inventoryBSTR(value string) (uintptr, error) {
	chars := utf16.Encode([]rune(value))
	var ptr *uint16
	if len(chars) > 0 {
		ptr = &chars[0]
	}
	bstr, _, _ := inventorySysAllocStringLen.Call(uintptr(unsafe.Pointer(ptr)), uintptr(len(chars)))
	runtime.KeepAlive(chars)
	if bstr == 0 {
		return 0, fmt.Errorf("WMI: cannot allocate BSTR")
	}
	return bstr, nil
}

func (v *inventoryVariant) clear() {
	inventoryVariantClear.Call(uintptr(unsafe.Pointer(v)))
}

func (v *inventoryVariant) text() (string, error) {
	switch v.kind {
	case 0, 1: // VT_EMPTY / VT_NULL: e.g. command line inaccessible or exited.
		return "", nil
	case 8: // VT_BSTR; use the length, not NUL termination or console encoding.
		if v.data[0] == 0 {
			return "", nil
		}
		ptr := *(**uint16)(unsafe.Pointer(&v.data[0]))
		n, _, _ := inventorySysStringLen.Call(uintptr(unsafe.Pointer(ptr)))
		return string(utf16.Decode(unsafe.Slice(ptr, int(n)))), nil
	default:
		return "", fmt.Errorf("WMI: expected string, got VARIANT type %d", v.kind)
	}
}

func (v *inventoryVariant) processID() (int, error) {
	// WMI represents CIM_UINT32 as VT_I4 (or VT_UI4); preserve the unsigned bits.
	if v.kind != 3 && v.kind != 19 {
		return 0, fmt.Errorf("WMI: expected process ID, got VARIANT type %d", v.kind)
	}
	n := uint32(v.data[0])
	if uint64(n) > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("WMI: process ID exceeds native int")
	}
	return int(n), nil
}

func (o *inventoryCOM) property(name string, value *inventoryVariant) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(o.vtable[4], uintptr(unsafe.Pointer(o)), uintptr(unsafe.Pointer(n)), 0, uintptr(unsafe.Pointer(value)), 0, 0)
	runtime.KeepAlive(n)
	return inventoryHRESULT("IWbemClassObject.Get("+name+")", hr)
}

func inventoryProxy(o *inventoryCOM) error {
	// Local WMI, current user: WINNT, no authorization service, CALL,
	// IMPERSONATE. Do not change the GUI's existing process-wide COM security.
	hr, _, _ := inventoryCoSetProxyBlanket.Call(uintptr(unsafe.Pointer(o)), 10, 0, 0, 3, 3, 0, 0)
	return inventoryHRESULT("CoSetProxyBlanket", hr)
}

// Best-effort cancellation complements finite Next waits and the caller's
// deadline. Guard the thread ID until cancellation stops, before returning the
// OS thread to Go. A stuck provider still has only one outstanding flight.
func inventoryCancellation(ctx context.Context) func() {
	hr, _, _ := inventoryCoEnableCancellation.Call(0)
	if inventoryHRESULT("CoEnableCallCancellation", hr) != nil {
		return func() {}
	}
	tid := windows.GetCurrentThreadId()
	var mu sync.Mutex
	active := true
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		// Cancellation can race the gap between two calls. Retry until cleanup.
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			mu.Lock()
			if active {
				inventoryCoCancelCall.Call(uintptr(tid), 0)
			}
			mu.Unlock()
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		mu.Lock()
		active = false
		close(done)
		mu.Unlock()
		inventoryCoDisableCancellation.Call(0)
	}
}

func queryScrcpyWMI(ctx context.Context) ([]scrcpyProc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := inventoryCoInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
	if err := inventoryHRESULT("CoInitializeEx", hr); err != nil {
		return nil, err
	}
	defer inventoryCoUninitialize.Call() // S_FALSE also requires balancing.
	inventorySecurityOnce.Do(func() {
		hr, _, _ := inventoryCoInitializeSecurity.Call(0, ^uintptr(0), 0, 0, 0, 3, 0, 0, 0)
		if uint32(hr) != 0x80010119 { // RPC_E_TOO_LATE: another GUI component initialized security.
			inventorySecurityErr = inventoryHRESULT("CoInitializeSecurity", hr)
		}
	})
	if inventorySecurityErr != nil {
		return nil, inventorySecurityErr
	}
	stopCancellation := inventoryCancellation(ctx)
	defer stopCancellation()

	var locator *inventoryCOM
	hr, _, _ = inventoryCoCreateInstance.Call(uintptr(unsafe.Pointer(&wbemLocatorCLSID)), 0, 1, uintptr(unsafe.Pointer(&wbemLocatorIID)), uintptr(unsafe.Pointer(&locator)))
	if err := inventoryHRESULT("CoCreateInstance(WbemLocator)", hr); err != nil {
		return nil, err
	}
	if locator == nil {
		return nil, fmt.Errorf("WMI: locator missing")
	}
	defer locator.release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	namespace, err := inventoryBSTR(`ROOT\CIMV2`)
	if err != nil {
		return nil, err
	}
	defer inventorySysFreeString.Call(namespace)
	var services *inventoryCOM
	hr, _, _ = syscall.SyscallN(locator.vtable[3], uintptr(unsafe.Pointer(locator)), namespace, 0, 0, 0, 0x80, 0, 0, uintptr(unsafe.Pointer(&services))) // CONNECT_USE_MAX_WAIT
	if err := inventoryHRESULT("IWbemLocator.ConnectServer", hr); err != nil {
		return nil, err
	}
	if services == nil {
		return nil, fmt.Errorf("WMI: services missing")
	}
	defer services.release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := inventoryProxy(services); err != nil {
		return nil, err
	}
	language, err := inventoryBSTR("WQL")
	if err != nil {
		return nil, err
	}
	defer inventorySysFreeString.Call(language)
	query, err := inventoryBSTR(scrcpyInventoryQuery)
	if err != nil {
		return nil, err
	}
	defer inventorySysFreeString.Call(query)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var enumerator *inventoryCOM
	hr, _, _ = syscall.SyscallN(services.vtable[20], uintptr(unsafe.Pointer(services)), language, query, 0x30, 0, uintptr(unsafe.Pointer(&enumerator))) // FORWARD_ONLY | RETURN_IMMEDIATELY
	if err := inventoryHRESULT("IWbemServices.ExecQuery", hr); err != nil {
		return nil, err
	}
	if enumerator == nil {
		return nil, fmt.Errorf("WMI: enumerator missing")
	}
	defer enumerator.release()
	if err := inventoryProxy(enumerator); err != nil {
		return nil, err
	}
	var procs []scrcpyProc
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var object *inventoryCOM
		var returned uint32
		hr, _, _ = syscall.SyscallN(enumerator.vtable[4], uintptr(unsafe.Pointer(enumerator)), 100, 1, uintptr(unsafe.Pointer(&object)), uintptr(unsafe.Pointer(&returned)))
		if err := inventoryHRESULT("IEnumWbemClassObject.Next", hr); err != nil {
			object.release()
			return nil, err
		}
		if returned != 0 && object != nil {
			proc, err := inventoryProcess(object)
			object.release()
			if err != nil {
				return nil, err
			}
			if proc.pid > 0 && proc.cmdline != "" && proc.createdMicros != 0 {
				procs = append(procs, proc)
			}
		}
		if uint32(hr) == 1 { // WBEM_S_FALSE: end, possibly with a final object.
			break
		}
		// WBEM_S_TIMEDOUT (0x40004) with no row is not end-of-enumeration.
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].pid < procs[j].pid })
	return procs, nil
}

func inventoryProcess(object *inventoryCOM) (scrcpyProc, error) {
	var values [4]inventoryVariant
	defer func() {
		for i := range values {
			values[i].clear()
		}
	}()
	for i, name := range []string{"ProcessId", "ParentProcessId", "CommandLine", "CreationDate"} {
		if err := object.property(name, &values[i]); err != nil {
			return scrcpyProc{}, err
		}
	}
	pid, err := values[0].processID()
	if err != nil {
		return scrcpyProc{}, err
	}
	ppid, err := values[1].processID()
	if err != nil {
		return scrcpyProc{}, err
	}
	cmdline, err := values[2].text()
	if err != nil {
		return scrcpyProc{}, err
	}
	created, err := values[3].text()
	if err != nil {
		return scrcpyProc{}, err
	}
	if cmdline == "" || created == "" {
		return scrcpyProc{}, nil // Inaccessible/exited process: do not guess ownership.
	}
	micros, err := inventoryCreationMicros(created)
	if err != nil {
		return scrcpyProc{}, err
	}
	return scrcpyProc{pid: pid, ppid: ppid, cmdline: cmdline, createdMicros: micros}, nil
}

func inventoryCreationMicros(value string) (int64, error) {
	// WMI's CIM_DATETIME: yyyymmddHHMMSS.mmmmmm+UUU (UTC offset in minutes).
	if len(value) != 25 || (value[21] != '+' && value[21] != '-') {
		return 0, fmt.Errorf("WMI: invalid process creation date %q", value)
	}
	t, err := time.Parse("20060102150405.000000", value[:21])
	if err != nil {
		return 0, fmt.Errorf("WMI: invalid process creation date: %w", err)
	}
	minutes, err := strconv.Atoi(value[22:])
	if err != nil || minutes < 0 {
		return 0, fmt.Errorf("WMI: invalid process creation UTC offset")
	}
	if value[21] == '-' {
		minutes = -minutes
	}
	return t.Add(-time.Duration(minutes) * time.Minute).UnixMicro(), nil
}

// A cached PID may have exited and been reused. Creation time must match the
// WMI row, and the handle must still refer to a live process. Keep the handle
// open across the final focus operation to preserve this process's identity.
func openInventoryProcess(proc scrcpyProc) (windows.Handle, error) {
	if proc.pid <= 0 || proc.createdMicros == 0 {
		return 0, fmt.Errorf("process identity missing")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(proc.pid))
	if err != nil {
		return 0, err
	}
	var created, exited, kernel, user windows.Filetime
	err = windows.GetProcessTimes(h, &created, &exited, &kernel, &user)
	if err == nil && created.Nanoseconds()/1000 != proc.createdMicros {
		err = fmt.Errorf("process ID reused")
	}
	if err == nil {
		var status uint32
		status, err = windows.WaitForSingleObject(h, 0)
		if err == nil && status != uint32(windows.WAIT_TIMEOUT) {
			err = fmt.Errorf("process exited")
		}
	}
	if err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

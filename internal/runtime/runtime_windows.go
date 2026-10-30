// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package runtime

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	goruntime "runtime"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx is MEMORYSTATUSEX, which x/sys/windows does not define.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// CPUCapacity returns the host's logical CPU capacity using runtime.NumCPU().
func CPUCapacity() (float64, error) {
	return float64(goruntime.NumCPU()), nil
}

// MemoryCapacity returns physical RAM in bytes, not currently free memory.
func MemoryCapacity() (uint64, error) {
	globalMemoryStatusEx := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	status := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 0, err
	}
	return status.totalPhys, nil
}

// Pipe returns a pipe whose ends are not inherited unless explicitly passed to
// a child. os.Pipe uses synchronous Windows pipe handles, as required by .NET's
// AnonymousPipeClientStream, but makes both ends inheritable.
func Pipe() (*os.File, *os.File, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	for _, f := range []*os.File{read, write} {
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			read.Close()
			write.Close()
			return nil, nil, err
		}
	}
	return read, write, nil
}

// ProcessIDs returns a snapshot of process IDs, including the current process
// and system processes. A process may exit before it is inspected.
func ProcessIDs() ([]int, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var pids []int
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		pids = append(pids, int(entry.ProcessID))
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return pids, nil
}

// LookupProcessEnv reads an environment variable from a process's PEB. Names
// are case-insensitive. The boolean distinguishes a missing variable from an
// empty value. Reading can fail due to permissions or process exit.
func LookupProcessEnv(pid int, name string) (string, bool, error) {
	if err := validateProcessEnvArgs(pid, name); err != nil {
		return "", false, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return "", false, err
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return "", false, err
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return "", false, os.ErrProcessDone
	}
	address, err := environmentAddress(handle)
	if err != nil {
		return "", false, err
	}
	return readProcessEnv(handle, address, name)
}

// environmentAddress follows PEB -> ProcessParameters -> Environment. The
// controller is amd64 or arm64, but a child can use the 32-bit WOW64 layout.
// ProcessWow64Information gives the 32-bit PEB directly; no guessed PEB offset
// is needed. The offsets are the Windows PEB/process-parameters layouts.
func environmentAddress(handle windows.Handle) (uintptr, error) {
	var basic windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&basic), uint32(unsafe.Sizeof(basic)), nil); err != nil {
		return 0, err
	}
	peb := uintptr(unsafe.Pointer(basic.PebBaseAddress))
	width := int(unsafe.Sizeof(uintptr(0)))
	if width == 8 {
		var wow64 uintptr
		if err := windows.NtQueryInformationProcess(handle, windows.ProcessWow64Information, unsafe.Pointer(&wow64), uint32(unsafe.Sizeof(wow64)), nil); err != nil {
			return 0, err
		}
		if wow64 != 0 {
			peb, width = wow64, 4
		}
	}
	parametersOffset, environmentOffset := uintptr(0x20), uintptr(0x80)
	if width == 4 {
		parametersOffset, environmentOffset = 0x10, 0x48
	}
	parameters, err := readRemotePointer(handle, peb+parametersOffset, width)
	if err != nil {
		return 0, err
	}
	return readRemotePointer(handle, parameters+environmentOffset, width)
}

func readRemotePointer(handle windows.Handle, address uintptr, width int) (uintptr, error) {
	var data [8]byte
	var read uintptr
	if err := windows.ReadProcessMemory(handle, address, &data[0], uintptr(width), &read); err != nil {
		return 0, err
	}
	if read != uintptr(width) {
		return 0, io.ErrUnexpectedEOF
	}
	var value uintptr
	if width == 4 {
		value = uintptr(binary.LittleEndian.Uint32(data[:4]))
	} else {
		value = uintptr(binary.LittleEndian.Uint64(data[:]))
	}
	if value == 0 {
		return 0, errors.New("null process environment pointer")
	}
	return value, nil
}

func readProcessEnv(handle windows.Handle, address uintptr, name string) (string, bool, error) {
	// Environment blocks are UTF-16, NUL-separated, with a double NUL at the
	// end. Read bounded chunks within committed regions, rather than trusting
	// an arbitrary process's allocation size or assuming a 32 KiB block.
	const maxEnvironment = 4 << 20
	var entry []uint16
	var data [4096]byte
	for total := uintptr(0); total < maxEnvironment; {
		var region windows.MemoryBasicInformation
		if err := windows.VirtualQueryEx(handle, address, &region, unsafe.Sizeof(region)); err != nil {
			return "", false, err
		}
		if address < region.BaseAddress || address-region.BaseAddress >= region.RegionSize {
			return "", false, errors.New("invalid process environment region")
		}
		size := min(uintptr(len(data)), region.RegionSize-(address-region.BaseAddress), maxEnvironment-total)
		if size < 2 || size%2 != 0 {
			return "", false, errors.New("unaligned process environment")
		}
		var read uintptr
		if err := windows.ReadProcessMemory(handle, address, &data[0], size, &read); err != nil {
			return "", false, err
		}
		if read != size {
			return "", false, io.ErrUnexpectedEOF
		}
		for offset := uintptr(0); offset < size; offset += 2 {
			unit := binary.LittleEndian.Uint16(data[offset : offset+2])
			if unit != 0 {
				entry = append(entry, unit)
				continue
			}
			if len(entry) == 0 {
				return "", false, nil
			}
			if value, found := lookupEnvEntry(string(utf16.Decode(entry)), name, true); found {
				return value, true, nil
			}
			entry = entry[:0]
		}
		address += size
		total += size
	}
	return "", false, errors.New("process environment exceeds read limit")
}

func testReadProcessEnv(env, name string) (string, bool, error) {
	data := utf16.Encode([]rune(env))
	value, found, err := readProcessEnv(windows.CurrentProcess(), uintptr(unsafe.Pointer(&data[0])), name)
	goruntime.KeepAlive(data)
	return value, found, err
}

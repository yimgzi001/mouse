// Throwaway spike: checks that cgo (MinGW gcc) can compile, link and call Media Foundation.
// It only enumerates capture devices by name; it never opens a camera stream.
package main

/*
#cgo LDFLAGS: -lmfplat -lmf -lmfreadwrite -lmfuuid -lole32 -luuid
#define COBJMACROS
#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mfreadwrite.h>

static int list_cameras(char *out, int cap) {
	int used = 0;
	HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
	if (FAILED(hr)) return -1;
	hr = MFStartup(MF_VERSION, MFSTARTUP_LITE);
	if (FAILED(hr)) return -2;
	IMFAttributes *attr = NULL;
	hr = MFCreateAttributes(&attr, 1);
	if (FAILED(hr)) return -3;
	hr = IMFAttributes_SetGUID(attr, &MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE,
		&MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP_GUID);
	if (FAILED(hr)) return -4;
	IMFActivate **devs = NULL;
	UINT32 count = 0;
	hr = MFEnumDeviceSources(attr, &devs, &count);
	if (FAILED(hr)) return -5;
	for (UINT32 i = 0; i < count; i++) {
		WCHAR *name = NULL;
		UINT32 len = 0;
		if (SUCCEEDED(IMFActivate_GetAllocatedString(devs[i],
				&MF_DEVSOURCE_ATTRIBUTE_FRIENDLY_NAME, &name, &len))) {
			int n = WideCharToMultiByte(CP_UTF8, 0, name, -1, out + used, cap - used - 2, NULL, NULL);
			if (n > 0) {
				used += n - 1;
				out[used++] = '\n';
				out[used] = 0;
			}
			CoTaskMemFree(name);
		}
		IMFActivate_Release(devs[i]);
	}
	CoTaskMemFree(devs);
	IMFAttributes_Release(attr);
	MFShutdown();
	CoUninitialize();
	return (int)count;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func main() {
	buf := make([]byte, 4096)
	n := int(C.list_cameras((*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
	fmt.Println("capture device count:", n)
	fmt.Print(C.GoString((*C.char)(unsafe.Pointer(&buf[0]))))
}

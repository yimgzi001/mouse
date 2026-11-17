#define COBJMACROS
#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mfreadwrite.h>
#include <mferror.h>
#include <stdlib.h>
#include <string.h>

#include "mfcam.h"

#define VIDEO_STREAM ((DWORD)MF_SOURCE_READER_FIRST_VIDEO_STREAM)

struct mfcam {
	IMFSourceReader *reader;
	int width, height;
	LONG stride; // bytes per row of the RGB32 output; negative means bottom-up
	int fps_num, fps_den;
	char format[8];
};

static int com_initialised = 0;

long mfcam_startup(void) {
	HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
	if (SUCCEEDED(hr)) {
		com_initialised = 1;
	} else if (hr != RPC_E_CHANGED_MODE) {
		return hr;
	}
	hr = MFStartup(MF_VERSION, MFSTARTUP_LITE);
	if (FAILED(hr)) {
		if (com_initialised) {
			CoUninitialize();
			com_initialised = 0;
		}
		return hr;
	}
	return MFCAM_OK;
}

void mfcam_shutdown(void) {
	MFShutdown();
	if (com_initialised) {
		CoUninitialize();
		com_initialised = 0;
	}
}

static HRESULT enum_devices(IMFActivate ***devs, UINT32 *count) {
	IMFAttributes *attr = NULL;
	HRESULT hr = MFCreateAttributes(&attr, 1);
	if (FAILED(hr)) return hr;
	hr = IMFAttributes_SetGUID(attr, &MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE,
		&MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP_GUID);
	if (SUCCEEDED(hr)) hr = MFEnumDeviceSources(attr, devs, count);
	IMFAttributes_Release(attr);
	return hr;
}

static void free_devices(IMFActivate **devs, UINT32 count) {
	for (UINT32 i = 0; i < count; i++) IMFActivate_Release(devs[i]);
	CoTaskMemFree(devs);
}

long mfcam_list(char *out, int cap) {
	IMFActivate **devs = NULL;
	UINT32 count = 0;
	int used = 0;
	if (cap > 0) out[0] = 0;
	HRESULT hr = enum_devices(&devs, &count);
	if (FAILED(hr)) return hr;
	for (UINT32 i = 0; i < count; i++) {
		WCHAR *name = NULL;
		UINT32 len = 0;
		if (SUCCEEDED(IMFActivate_GetAllocatedString(devs[i], &MF_DEVSOURCE_ATTRIBUTE_FRIENDLY_NAME, &name, &len))) {
			int room = cap - used - 2;
			if (room > 0) {
				int n = WideCharToMultiByte(CP_UTF8, 0, name, -1, out + used, room, NULL, NULL);
				if (n > 0) used += n - 1;
			}
			CoTaskMemFree(name);
		}
		if (used + 1 < cap) {
			out[used++] = '\n';
			out[used] = 0;
		}
	}
	free_devices(devs, count);
	return (long)count;
}

static void subtype_name(const GUID *g, char *out) {
	if (IsEqualGUID(g, &MFVideoFormat_RGB32)) {
		strcpy(out, "RGB32");
		return;
	}
	if (IsEqualGUID(g, &MFVideoFormat_RGB24)) {
		strcpy(out, "RGB24");
		return;
	}
	DWORD f = g->Data1;
	for (int i = 0; i < 4; i++) {
		char ch = (char)((f >> (8 * i)) & 0xff);
		out[i] = (ch >= 32 && ch < 127) ? ch : '?';
	}
	out[4] = 0;
}

static int is_raw(const GUID *g) {
	return IsEqualGUID(g, &MFVideoFormat_YUY2) || IsEqualGUID(g, &MFVideoFormat_NV12) ||
		IsEqualGUID(g, &MFVideoFormat_RGB24) || IsEqualGUID(g, &MFVideoFormat_RGB32) ||
		IsEqualGUID(g, &MFVideoFormat_UYVY) || IsEqualGUID(g, &MFVideoFormat_I420) ||
		IsEqualGUID(g, &MFVideoFormat_IYUV);
}

// pick_native_type chooses the camera mode closest to the wanted size and rate,
// preferring uncompressed formats, and returns it with a reference held.
static HRESULT pick_native_type(IMFSourceReader *reader, int want_w, int want_h, int want_fps,
		IMFMediaType **picked, char *format, int *fps_num, int *fps_den) {
	double best_cost = 1e30;
	*picked = NULL;
	for (DWORD i = 0;; i++) {
		IMFMediaType *t = NULL;
		HRESULT hr = IMFSourceReader_GetNativeMediaType(reader, VIDEO_STREAM, i, &t);
		if (FAILED(hr)) break;
		GUID sub;
		UINT64 size = 0, rate = 0;
		if (FAILED(IMFMediaType_GetGUID(t, &MF_MT_SUBTYPE, &sub)) ||
				FAILED(IMFMediaType_GetUINT64(t, &MF_MT_FRAME_SIZE, &size))) {
			IMFMediaType_Release(t);
			continue;
		}
		int w = (int)(size >> 32), h = (int)(size & 0xffffffff);
		int num = 30, den = 1;
		if (SUCCEEDED(IMFMediaType_GetUINT64(t, &MF_MT_FRAME_RATE, &rate)) && (rate & 0xffffffff) != 0) {
			num = (int)(rate >> 32);
			den = (int)(rate & 0xffffffff);
		}
		double fps = (double)num / (double)den;
		double cost = 4.0 * (abs(w - want_w) + abs(h - want_h));
		cost += fps < want_fps ? 20.0 * (want_fps - fps) : (fps - want_fps);
		if (!is_raw(&sub)) cost += IsEqualGUID(&sub, &MFVideoFormat_MJPG) ? 5.0 : 1000.0;
		if (cost < best_cost) {
			best_cost = cost;
			if (*picked) IMFMediaType_Release(*picked);
			*picked = t;
			subtype_name(&sub, format);
			*fps_num = num;
			*fps_den = den;
		} else {
			IMFMediaType_Release(t);
		}
	}
	return *picked ? S_OK : MF_E_INVALIDMEDIATYPE;
}

// refresh_output_format reads the size and row stride of the RGB32 output.
static HRESULT refresh_output_format(mfcam *c) {
	IMFMediaType *cur = NULL;
	HRESULT hr = IMFSourceReader_GetCurrentMediaType(c->reader, VIDEO_STREAM, &cur);
	if (FAILED(hr)) return hr;
	UINT64 size = 0;
	hr = IMFMediaType_GetUINT64(cur, &MF_MT_FRAME_SIZE, &size);
	if (SUCCEEDED(hr)) {
		c->width = (int)(size >> 32);
		c->height = (int)(size & 0xffffffff);
		UINT32 stride = 0;
		if (SUCCEEDED(IMFMediaType_GetUINT32(cur, &MF_MT_DEFAULT_STRIDE, &stride))) {
			c->stride = (LONG)stride;
		} else {
			LONG s = 0;
			if (SUCCEEDED(MFGetStrideForBitmapInfoHeader(MFVideoFormat_RGB32.Data1, (DWORD)c->width, &s))) {
				c->stride = s;
			} else {
				c->stride = c->width * 4;
			}
		}
	}
	IMFMediaType_Release(cur);
	return hr;
}

long mfcam_open(int index, int want_w, int want_h, int want_fps, mfcam **cam) {
	*cam = NULL;
	IMFActivate **devs = NULL;
	UINT32 count = 0;
	HRESULT hr = enum_devices(&devs, &count);
	if (FAILED(hr)) return hr;
	if (index < 0 || (UINT32)index >= count) {
		free_devices(devs, count);
		return MF_E_NOT_FOUND;
	}
	IMFMediaSource *source = NULL;
	hr = IMFActivate_ActivateObject(devs[index], &IID_IMFMediaSource, (void **)&source);
	free_devices(devs, count);
	if (FAILED(hr)) return hr;

	IMFAttributes *attr = NULL;
	IMFSourceReader *reader = NULL;
	hr = MFCreateAttributes(&attr, 1);
	// Let the reader insert the decoder and colour converter needed for RGB32 output.
	if (SUCCEEDED(hr)) hr = IMFAttributes_SetUINT32(attr, &MF_SOURCE_READER_ENABLE_VIDEO_PROCESSING, TRUE);
	if (SUCCEEDED(hr)) hr = MFCreateSourceReaderFromMediaSource(source, attr, &reader);
	if (attr) IMFAttributes_Release(attr);
	if (FAILED(hr)) {
		IMFMediaSource_Shutdown(source);
		IMFMediaSource_Release(source);
		return hr;
	}
	// The reader now owns the source and shuts it down when released.
	IMFMediaSource_Release(source);

	mfcam *c = (mfcam *)calloc(1, sizeof(mfcam));
	if (!c) {
		IMFSourceReader_Release(reader);
		return E_OUTOFMEMORY;
	}
	c->reader = reader;

	IMFMediaType *native = NULL;
	hr = pick_native_type(reader, want_w, want_h, want_fps, &native, c->format, &c->fps_num, &c->fps_den);
	if (SUCCEEDED(hr)) {
		hr = IMFSourceReader_SetCurrentMediaType(reader, VIDEO_STREAM, NULL, native);
		IMFMediaType_Release(native);
	}
	if (SUCCEEDED(hr)) {
		IMFMediaType *out = NULL;
		hr = MFCreateMediaType(&out);
		if (SUCCEEDED(hr)) hr = IMFMediaType_SetGUID(out, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
		if (SUCCEEDED(hr)) hr = IMFMediaType_SetGUID(out, &MF_MT_SUBTYPE, &MFVideoFormat_RGB32);
		if (SUCCEEDED(hr)) hr = IMFSourceReader_SetCurrentMediaType(reader, VIDEO_STREAM, NULL, out);
		if (out) IMFMediaType_Release(out);
	}
	if (SUCCEEDED(hr)) hr = refresh_output_format(c);
	if (SUCCEEDED(hr) && (c->width <= 0 || c->height <= 0)) hr = MF_E_INVALIDMEDIATYPE;
	if (FAILED(hr)) {
		mfcam_close(c);
		return hr;
	}
	*cam = c;
	return MFCAM_OK;
}

void mfcam_info(mfcam *c, int *w, int *h, int *fps_num, int *fps_den, char *format) {
	*w = c->width;
	*h = c->height;
	*fps_num = c->fps_num;
	*fps_den = c->fps_den;
	strcpy(format, c->format);
}

long mfcam_read(mfcam *c, unsigned char *dst, int dst_cap, int mirror) {
	DWORD stream = 0, flags = 0;
	LONGLONG ts = 0;
	IMFSample *sample = NULL;
	HRESULT hr = IMFSourceReader_ReadSample(c->reader, VIDEO_STREAM, 0, &stream, &flags, &ts, &sample);
	if (FAILED(hr)) return hr;
	if (flags & (MF_SOURCE_READERF_ERROR | MF_SOURCE_READERF_ENDOFSTREAM)) {
		if (sample) IMFSample_Release(sample);
		return MF_E_VIDEO_RECORDING_DEVICE_INVALIDATED;
	}
	if (flags & MF_SOURCE_READERF_CURRENTMEDIATYPECHANGED) {
		hr = refresh_output_format(c);
		if (FAILED(hr)) {
			if (sample) IMFSample_Release(sample);
			return hr;
		}
	}
	if (!sample) return MFCAM_NO_FRAME;

	int w = c->width, h = c->height;
	if (dst_cap < w * h * 3) {
		IMFSample_Release(sample);
		return E_NOT_SUFFICIENT_BUFFER;
	}

	IMFMediaBuffer *buf = NULL;
	hr = IMFSample_ConvertToContiguousBuffer(sample, &buf);
	IMFSample_Release(sample);
	if (FAILED(hr)) return hr;

	BYTE *scan0 = NULL;
	LONG pitch = 0;
	IMF2DBuffer *buf2d = NULL;
	int locked2d = 0;
	if (SUCCEEDED(IMFMediaBuffer_QueryInterface(buf, &IID_IMF2DBuffer, (void **)&buf2d))) {
		if (SUCCEEDED(IMF2DBuffer_Lock2D(buf2d, &scan0, &pitch))) {
			locked2d = 1;
		} else {
			IMF2DBuffer_Release(buf2d);
			buf2d = NULL;
		}
	}
	if (!locked2d) {
		BYTE *data = NULL;
		DWORD max_len = 0, cur_len = 0;
		hr = IMFMediaBuffer_Lock(buf, &data, &max_len, &cur_len);
		if (FAILED(hr)) {
			IMFMediaBuffer_Release(buf);
			return hr;
		}
		pitch = c->stride;
		LONG abs_pitch = pitch < 0 ? -pitch : pitch;
		if (abs_pitch < w * 4 || (DWORD)(abs_pitch * h) > cur_len) {
			IMFMediaBuffer_Unlock(buf);
			IMFMediaBuffer_Release(buf);
			return MF_E_BUFFERTOOSMALL;
		}
		scan0 = pitch < 0 ? data + (size_t)abs_pitch * (size_t)(h - 1) : data;
	}

	for (int y = 0; y < h; y++) {
		const BYTE *src = scan0 + (ptrdiff_t)pitch * y;
		unsigned char *out = dst + (size_t)y * (size_t)w * 3;
		if (!mirror) {
			for (int x = 0; x < w; x++) {
				out[0] = src[2];
				out[1] = src[1];
				out[2] = src[0];
				src += 4;
				out += 3;
			}
		} else {
			src += (size_t)(w - 1) * 4;
			for (int x = 0; x < w; x++) {
				out[0] = src[2];
				out[1] = src[1];
				out[2] = src[0];
				src -= 4;
				out += 3;
			}
		}
	}

	if (locked2d) {
		IMF2DBuffer_Unlock2D(buf2d);
		IMF2DBuffer_Release(buf2d);
	} else {
		IMFMediaBuffer_Unlock(buf);
	}
	IMFMediaBuffer_Release(buf);
	return MFCAM_OK;
}

void mfcam_close(mfcam *c) {
	if (!c) return;
	if (c->reader) IMFSourceReader_Release(c->reader);
	free(c);
}

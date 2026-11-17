#ifndef GESTURECTL_MFCAM_H
#define GESTURECTL_MFCAM_H

// Minimal webcam capture on top of Windows Media Foundation.
// All functions must be called from the same OS thread.

typedef struct mfcam mfcam;

// Results other than MFCAM_OK / MFCAM_NO_FRAME are HRESULT failure codes.
#define MFCAM_OK 0
#define MFCAM_NO_FRAME 1

// mfcam_startup initialises COM and Media Foundation for the calling thread.
long mfcam_startup(void);
void mfcam_shutdown(void);

// mfcam_list writes the friendly names of the capture devices into out,
// one per line (UTF-8), and returns how many devices exist or a negative HRESULT.
long mfcam_list(char *out, int cap);

// mfcam_open opens device number index and asks for the capture mode closest to
// the wanted size and frame rate. On success *cam is set.
long mfcam_open(int index, int want_w, int want_h, int want_fps, mfcam **cam);

// mfcam_info reports the negotiated mode. format receives a short name such as
// "YUY2" or "MJPG" (at least 8 bytes).
void mfcam_info(mfcam *cam, int *w, int *h, int *fps_num, int *fps_den, char *format);

// mfcam_read blocks until the next frame and writes it to dst as packed RGB
// (w*h*3 bytes, top row first). When mirror is non-zero the image is flipped
// horizontally. Returns MFCAM_OK, MFCAM_NO_FRAME (try again) or a failure code.
long mfcam_read(mfcam *cam, unsigned char *dst, int dst_cap, int mirror);

void mfcam_close(mfcam *cam);

#endif

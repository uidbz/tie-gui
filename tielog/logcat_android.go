//go:build android

package tielog

/*
#include <android/log.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

// androidLog mirrors every line to logcat, the only log stream reachable on a
// device without a shell session. stderr exists in the Android build but goes
// nowhere a user or a developer can read, and the log file sits inside the app
// sandbox, so without this the apps are silent exactly where they are hardest
// to debug.
var androidLog = func(tag, msg string) {
	ct := C.CString(tag)
	cm := C.CString(msg)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cm))
	C.__android_log_write(C.ANDROID_LOG_INFO, ct, cm)
}

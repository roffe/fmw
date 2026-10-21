//go:build android

package fmw

/*
#include <errno.h>
#include <stdint.h>
#include <jni.h>
#include <libavcodec/jni.h>

// set_android_app gives FFmpeg the JavaVM and a global reference to the
// application context of ctx, as it opens content:// URIs through them.
static int set_android_app(uintptr_t vm, uintptr_t env, uintptr_t ctx) {
	JNIEnv *e = (JNIEnv *)env;
	jclass cls = (*e)->GetObjectClass(e, (jobject)ctx);
	jmethodID get = (*e)->GetMethodID(e, cls, "getApplicationContext", "()Landroid/content/Context;");
	(*e)->DeleteLocalRef(e, cls);
	jobject app = get ? (*e)->CallObjectMethod(e, (jobject)ctx, get) : NULL;
	if ((*e)->ExceptionCheck(e)) {
		(*e)->ExceptionClear(e);
		return -EINVAL;
	}
	if (!app)
		return -EINVAL;
	jobject global = (*e)->NewGlobalRef(e, app);
	(*e)->DeleteLocalRef(e, app);
	int ret = av_jni_set_java_vm((void *)vm, NULL);
	return ret < 0 ? ret : av_jni_set_android_app_ctx(global, NULL);
}
*/
import "C"

import (
	"sync"

	"fyne.io/fyne/v2/driver"
)

// queuedReads is how many of the mixer's reads oto's driver queues ahead of
// the device: on Android it reads three device buffers at a time into a FIFO
// holding two such reads.
const queuedReads = 2

// platformReady gives FFmpeg the app's JVM, once, before anything opens.
var platformReady = sync.OnceValue(func() error {
	return driver.RunNative(func(ctx any) error {
		c := ctx.(*driver.AndroidContext)
		if ret := C.set_android_app(C.uintptr_t(c.VM), C.uintptr_t(c.Env), C.uintptr_t(c.Ctx)); ret < 0 {
			return averror(ret)
		}
		return nil
	})
})

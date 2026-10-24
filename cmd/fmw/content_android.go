//go:build android

package main

/*
#include <stdarg.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <jni.h>

static jobject call(JNIEnv *e, jobject obj, const char *name, const char *sig, ...) {
	jclass cls = (*e)->GetObjectClass(e, obj);
	jmethodID m = (*e)->GetMethodID(e, cls, name, sig);
	if (!m)
		return NULL;
	va_list args;
	va_start(args, sig);
	jobject ret = (*e)->CallObjectMethodV(e, obj, m, args);
	va_end(args);
	return (*e)->ExceptionCheck(e) ? NULL : ret;
}

static jobject parse_uri(JNIEnv *e, const char *uri) {
	jclass cls = (*e)->FindClass(e, "android/net/Uri");
	jmethodID parse = (*e)->GetStaticMethodID(e, cls, "parse", "(Ljava/lang/String;)Landroid/net/Uri;");
	jobject u = (*e)->CallStaticObjectMethod(e, cls, parse, (*e)->NewStringUTF(e, uri));
	return (*e)->ExceptionCheck(e) ? NULL : u;
}

// keep_readable takes the persistable read grant the document picker gave
// for uri, so it still opens after the app restarts.
static void keep_readable(uintptr_t env, uintptr_t ctx, const char *uri) {
	JNIEnv *e = (JNIEnv *)env;
	if ((*e)->PushLocalFrame(e, 16) < 0)
		return;
	jobject resolver = call(e, (jobject)ctx, "getContentResolver", "()Landroid/content/ContentResolver;");
	jobject u = resolver ? parse_uri(e, uri) : NULL;
	if (u) {
		jclass cls = (*e)->GetObjectClass(e, resolver);
		jmethodID take = (*e)->GetMethodID(e, cls, "takePersistableUriPermission", "(Landroid/net/Uri;I)V");
		(*e)->CallVoidMethod(e, resolver, take, u, 1); // Intent.FLAG_GRANT_READ_URI_PERMISSION
	}
	(*e)->ExceptionClear(e);
	(*e)->PopLocalFrame(e, NULL);
}

// display_name asks the content resolver of ctx for the display name of uri,
// NULL when it has none or the app may not read it.
static char *display_name(uintptr_t env, uintptr_t ctx, const char *uri) {
	JNIEnv *e = (JNIEnv *)env;
	if ((*e)->PushLocalFrame(e, 16) < 0)
		return NULL;
	char *name = NULL;
	jobject resolver = call(e, (jobject)ctx, "getContentResolver", "()Landroid/content/ContentResolver;");
	jobject u = resolver ? parse_uri(e, uri) : NULL;
	jobjectArray proj = u ? (*e)->NewObjectArray(e, 1, (*e)->FindClass(e, "java/lang/String"), (*e)->NewStringUTF(e, "_display_name")) : NULL;
	jobject cursor = proj ? call(e, resolver, "query",
		"(Landroid/net/Uri;[Ljava/lang/String;Ljava/lang/String;[Ljava/lang/String;Ljava/lang/String;)Landroid/database/Cursor;",
		u, proj, NULL, NULL, NULL) : NULL;
	if (cursor) {
		jclass cls = (*e)->GetObjectClass(e, cursor);
		if ((*e)->CallBooleanMethod(e, cursor, (*e)->GetMethodID(e, cls, "moveToFirst", "()Z")) && !(*e)->ExceptionCheck(e)) {
			jstring s = call(e, cursor, "getString", "(I)Ljava/lang/String;", 0);
			const char *c = s ? (*e)->GetStringUTFChars(e, s, NULL) : NULL;
			if (c) {
				name = strdup(c);
				(*e)->ReleaseStringUTFChars(e, s, c);
			}
		}
		(*e)->ExceptionClear(e);
		(*e)->CallVoidMethod(e, cursor, (*e)->GetMethodID(e, cls, "close", "()V"));
	}
	(*e)->ExceptionClear(e);
	(*e)->PopLocalFrame(e, NULL);
	return name;
}
*/
import "C"

import (
	"strings"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2/driver"
)

var contentNames sync.Map

// contentName is the file name Android shows for a content:// URI, or "".
func contentName(uri string) string {
	if !strings.HasPrefix(uri, "content://") {
		return ""
	}
	if name, ok := contentNames.Load(uri); ok {
		return name.(string)
	}
	var name string
	withURI(uri, func(env, ctx C.uintptr_t, u *C.char) {
		if p := C.display_name(env, ctx, u); p != nil {
			name = C.GoString(p)
			C.free(unsafe.Pointer(p))
		}
	})
	contentNames.Store(uri, name)
	return name
}

// keepContent keeps a picked content:// URI readable across restarts.
// ponytail: grants are never released, Android drops the oldest past its limit.
func keepContent(uri string) {
	if strings.HasPrefix(uri, "content://") {
		withURI(uri, func(env, ctx C.uintptr_t, u *C.char) { C.keep_readable(env, ctx, u) })
	}
}

func withURI(uri string, f func(env, ctx C.uintptr_t, u *C.char)) {
	driver.RunNative(func(ctx any) error {
		c := ctx.(*driver.AndroidContext)
		u := C.CString(uri)
		defer C.free(unsafe.Pointer(u))
		f(C.uintptr_t(c.Env), C.uintptr_t(c.Ctx), u)
		return nil
	})
}

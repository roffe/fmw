.PHONY: deps linux linux-static appimage install-linux windows macos android install-android clean

VERSION=$(shell sed -n 's/^Version = "\(.*\)"/\1/p' cmd/fmw/FyneApp.toml)
FFMPEG=https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n9.0-latest
VULKAN_HEADERS=vulkan-sdk-1.4.363.0
FFLIBS=avformat avcodec avutil swscale swresample
WINCC?=x86_64-w64-mingw32-clang
WINCXX?=x86_64-w64-mingw32-clang++
PROJECTM=4.1.6
GLEW=2.3.1
DAV1D=1.5.4
LIBDRM=2.4.134
LIBVA=2.24.1
NVCODEC=n13.1.15.0
OPENSSL=3.6.5
STATIC_MESON=--prefix=$(CURDIR)/.tmp/static-linux64.part --libdir=lib --buildtype=release -Ddefault_library=static
PRESETS=https://github.com/projectM-visualizer/presets-cream-of-the-crop/archive/0180df21f5e0bd39b9060cc5de420ed2f1f9e509.tar.gz
TEXTURES=https://github.com/projectM-visualizer/presets-milkdrop-texture-pack/archive/6368812f27bc747b517218fbf89d21d59afce4d9.tar.gz
PROJECTM_CMAKE=-DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_LIBDIR=lib -DENABLE_SYSTEM_PROJECTM_EVAL=OFF -DBUILD_TESTING=OFF -DCMAKE_POLICY_VERSION_MINIMUM=3.5
FFMPEG_SRC=https://ffmpeg.org/releases/ffmpeg-9.0.tar.xz
FFMPEG_PATCHES=$(sort $(wildcard patches/ffmpeg/*.patch))
NDK?=$(HOME)/Android/android-ndk-r30
NDKBIN=$(NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin
ANDROID_API=21
ADB?=$(HOME)/Android/platform-tools/adb
NPROC=$(shell nproc 2>/dev/null || sysctl -n hw.ncpu)
PREFIX?=$(HOME)/.local
MIMETYPES=application/ogg;application/x-ogg;application/mxf;application/vnd.rn-realmedia;application/vnd.rn-realmedia-vbr;audio/aac;audio/x-aac;audio/vnd.dolby.heaac.1;audio/vnd.dolby.heaac.2;audio/aiff;audio/x-aiff;audio/m4a;audio/x-m4a;application/x-extension-m4a;audio/mp1;audio/x-mp1;audio/mp2;audio/x-mp2;audio/mp3;audio/x-mp3;audio/mpeg;audio/mpeg2;audio/mpeg3;audio/mpegurl;audio/x-mpegurl;audio/mpg;audio/x-mpg;audio/rn-mpeg;audio/musepack;audio/x-musepack;audio/ogg;audio/vnd.rn-realaudio;audio/wav;audio/x-pn-wav;audio/x-pn-windows-pcm;audio/x-realaudio;audio/x-pn-realaudio;audio/x-ms-wma;audio/x-wav;video/mpeg;video/x-mpeg2;video/x-mpeg3;video/mp4v-es;video/x-m4v;video/mp4;application/x-extension-mp4;video/divx;video/vnd.divx;video/msvideo;video/x-msvideo;video/ogg;video/quicktime;video/vnd.rn-realvideo;video/x-ms-afs;video/x-ms-asf;audio/x-ms-asf;application/vnd.ms-asf;video/x-ms-wmv;video/x-ms-wmx;video/x-ms-wvxvideo;video/x-avi;video/avi;video/x-flic;video/fli;video/x-flc;video/flv;video/x-flv;video/x-theora;video/x-theora+ogg;video/x-matroska;video/mkv;audio/x-matroska;application/x-matroska;video/webm;audio/webm;audio/vorbis;audio/x-vorbis;audio/x-vorbis+ogg;video/x-ogm;video/x-ogm+ogg;application/x-ogm;application/x-ogm-audio;application/x-ogm-video;application/x-shorten;audio/x-shorten;audio/x-ape;audio/x-wavpack;audio/x-tta;audio/AMR;audio/ac3;audio/eac3;audio/amr-wb;video/mp2t;audio/flac;audio/mp4;application/x-mpegurl;video/vnd.mpegurl;application/vnd.apple.mpegurl;audio/x-pn-au;video/3gp;video/3gpp;video/3gpp2;audio/3gpp;audio/3gpp2;video/dv;audio/dv;audio/opus;audio/vnd.dts;audio/vnd.dts.hd;audio/x-adpcm;audio/m3u;audio/vnd.wave;video/vnd.avi;
DESKTOP=[Desktop Entry]\nType=Application\nName=fmw\nIcon=com.roffe.fmw\nCategories=AudioVideo;Video;Player;\nMimeType=$(MIMETYPES)\n

# The releases build against FFmpeg from BtbN and projectM built here, and
# bundle them, not the system's
export GOWORK=off
export CGO_CFLAGS=-O2 -g -I$(CURDIR)/.tmp/Vulkan-Headers/include

default: linux

ifeq ($(shell uname -s),Darwin)
deps: .tmp/macos
else
deps: .tmp/linux64 .tmp/Vulkan-Headers .tmp/projectm-linux64 .tmp/presets .tmp/textures
endif

.tmp/linux64:
	rm -rf $@.part
	mkdir -p $@.part
	curl -fsSL $(FFMPEG)-linux64-lgpl-shared-9.0.tar.xz | tar -xJ --strip-components=1 -C $@.part
	mv $@.part $@

.tmp/win64:
	mkdir -p .tmp
	curl -fsSL -o .tmp/win64.zip $(FFMPEG)-win64-lgpl-shared-9.0.zip
	unzip -q -d .tmp .tmp/win64.zip
	mv .tmp/ffmpeg-n9.0-latest-win64-lgpl-shared-9.0 $@
	rm .tmp/win64.zip

# BtbN has no Android builds. Static, as fyne package bundles no libraries
# but fmw's own, and at the API level fyne builds that with. JNI for the
# content:// URIs Android's pickers give and for MediaCodec.
.tmp/android-arm64: .tmp/Vulkan-Headers
	rm -rf $@.part .tmp/ffmpeg-android
	mkdir -p .tmp/ffmpeg-android
	curl -fsSL $(FFMPEG_SRC) | tar -xJ --strip-components=1 -C .tmp/ffmpeg-android
	cd .tmp/ffmpeg-android && ./configure --prefix=$(CURDIR)/$@.part --target-os=android --arch=aarch64 --enable-cross-compile \
		--cc=$(NDKBIN)/aarch64-linux-android$(ANDROID_API)-clang --cxx=$(NDKBIN)/aarch64-linux-android$(ANDROID_API)-clang++ \
		--ar=$(NDKBIN)/llvm-ar --nm=$(NDKBIN)/llvm-nm --ranlib=$(NDKBIN)/llvm-ranlib --strip=$(NDKBIN)/llvm-strip \
		--enable-static --disable-shared --enable-pic --enable-jni --enable-mediacodec --enable-vulkan --extra-cflags=-I$(CURDIR)/.tmp/Vulkan-Headers/include \
		--disable-programs --disable-doc --disable-avdevice --disable-avfilter
	$(MAKE) -C .tmp/ffmpeg-android -j$(NPROC) install
	sed -i 's|$(CURDIR)/$@.part|$${pcfiledir}/../..|' $@.part/lib/pkgconfig/*.pc
	cp .tmp/ffmpeg-android/COPYING.LGPLv2.1 $@.part/LICENSE.txt
	mv $@.part $@

# BtbN has no macOS builds either. Static, so fmw needs no FFmpeg installed.
# configure finds VideoToolbox for hardware decoding and Secure Transport for
# https by itself.
.tmp/macos: $(FFMPEG_PATCHES)
	rm -rf $@.part .tmp/ffmpeg-macos
	mkdir -p .tmp/ffmpeg-macos
	curl -fsSL $(FFMPEG_SRC) | tar -xJ --strip-components=1 -C .tmp/ffmpeg-macos
	for p in $(FFMPEG_PATCHES); do patch -p1 -d .tmp/ffmpeg-macos < $$p || exit 1; done
	cd .tmp/ffmpeg-macos && ./configure --prefix=$(CURDIR)/$@.part --enable-static --disable-shared --enable-pic \
		--disable-programs --disable-doc --disable-avdevice --disable-avfilter --disable-encoders --disable-muxers
	$(MAKE) -C .tmp/ffmpeg-macos -j$(NPROC) install
	sed -i.bak 's|$(CURDIR)/$@.part|$${pcfiledir}/../..|g' $@.part/lib/pkgconfig/*.pc
	rm $@.part/lib/pkgconfig/*.bak
	cp .tmp/ffmpeg-macos/COPYING.LGPLv2.1 $@.part/LICENSE.txt
	rm -rf $@
	mv $@.part $@

# FFmpeg for make linux-static, with only what fmw needs to play files and
# streams, and the libraries for that, all static: dav1d, VAAPI, NVDEC and
# Vulkan, which load their drivers at run time, and OpenSSL for https. libva
# looks for VA drivers where Debian, Fedora and Arch keep them.
.tmp/static-linux64: .tmp/Vulkan-Headers
	rm -rf $@.part .tmp/static-src
	mkdir -p $@.part/licenses .tmp/static-src/dav1d .tmp/static-src/drm .tmp/static-src/va .tmp/static-src/nvcodec .tmp/static-src/openssl .tmp/static-src/ffmpeg
	curl -fsSL https://downloads.videolan.org/pub/videolan/dav1d/$(DAV1D)/dav1d-$(DAV1D).tar.xz | tar -xJ --strip-components=1 -C .tmp/static-src/dav1d
	curl -fsSL https://dri.freedesktop.org/libdrm/libdrm-$(LIBDRM).tar.xz | tar -xJ --strip-components=1 -C .tmp/static-src/drm
	curl -fsSL https://github.com/intel/libva/archive/refs/tags/$(LIBVA).tar.gz | tar -xz --strip-components=1 -C .tmp/static-src/va
	curl -fsSL https://github.com/FFmpeg/nv-codec-headers/archive/refs/tags/$(NVCODEC).tar.gz | tar -xz --strip-components=1 -C .tmp/static-src/nvcodec
	curl -fsSL https://github.com/openssl/openssl/releases/download/openssl-$(OPENSSL)/openssl-$(OPENSSL).tar.gz | tar -xz --strip-components=1 -C .tmp/static-src/openssl
	curl -fsSL $(FFMPEG_SRC) | tar -xJ --strip-components=1 -C .tmp/static-src/ffmpeg
	meson setup .tmp/static-src/dav1d/build .tmp/static-src/dav1d $(STATIC_MESON) -Denable_tools=false -Denable_tests=false
	ninja -C .tmp/static-src/dav1d/build install
	meson setup .tmp/static-src/drm/build .tmp/static-src/drm $(STATIC_MESON) -Dintel=disabled -Dradeon=disabled -Damdgpu=disabled \
		-Dnouveau=disabled -Dvmwgfx=disabled -Dcairo-tests=disabled -Dman-pages=disabled -Dvalgrind=disabled -Dtests=false
	ninja -C .tmp/static-src/drm/build install
	sed -i 's/shared_library(/library(/' .tmp/static-src/va/va/meson.build # else libva makes shared libraries only
	PKG_CONFIG_PATH=$(CURDIR)/$@.part/lib/pkgconfig meson setup .tmp/static-src/va/build .tmp/static-src/va $(STATIC_MESON) \
		-Ddriverdir=/usr/lib/x86_64-linux-gnu/dri:/usr/lib64/dri:/usr/lib/dri -Dwith_x11=no -Dwith_glx=no -Dwith_wayland=no -Denable_docs=false
	ninja -C .tmp/static-src/va/build install
	$(MAKE) -C .tmp/static-src/nvcodec PREFIX=$(CURDIR)/$@.part install
	cd .tmp/static-src/openssl && ./Configure linux-x86_64 no-shared no-tests --prefix=$(CURDIR)/$@.part --libdir=lib --openssldir=/etc/ssl
	$(MAKE) -C .tmp/static-src/openssl -j$(NPROC) build_libs
	$(MAKE) -C .tmp/static-src/openssl install_dev
	cd .tmp/static-src/ffmpeg && PKG_CONFIG_PATH=$(CURDIR)/$@.part/lib/pkgconfig ./configure --prefix=$(CURDIR)/$@.part --pkg-config-flags=--static \
		--enable-static --disable-shared --enable-pic --enable-version3 --disable-autodetect --disable-programs --disable-doc \
		--disable-avdevice --disable-avfilter --disable-encoders --disable-muxers \
		--enable-pthreads --enable-zlib --enable-iconv --enable-openssl --enable-libdav1d \
		--enable-libdrm --enable-vaapi --enable-ffnvcodec --enable-cuda --enable-nvdec --enable-vulkan \
		--extra-cflags=-I$(CURDIR)/.tmp/Vulkan-Headers/include
	$(MAKE) -C .tmp/static-src/ffmpeg -j$(NPROC) install
	sed -i 's|$(CURDIR)/$@.part|$${pcfiledir}/../..|g' $@.part/lib/pkgconfig/*.pc
	cp .tmp/static-src/ffmpeg/COPYING.LGPLv3 $@.part/licenses/FFmpeg-LICENSE.txt
	cp .tmp/static-src/dav1d/COPYING $@.part/licenses/dav1d-LICENSE.txt
	cp .tmp/static-src/va/COPYING $@.part/licenses/libva-LICENSE.txt
	cp .tmp/static-src/openssl/LICENSE.txt $@.part/licenses/OpenSSL-LICENSE.txt
	mv $@.part $@

.tmp/Vulkan-Headers:
	git clone -q --depth 1 -b $(VULKAN_HEADERS) https://github.com/KhronosGroup/Vulkan-Headers $@

.tmp/appimagetool:
	@mkdir -p .tmp
	curl -fsSL -o $@ https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
	chmod +x $@

.tmp/projectm-src:
	rm -rf $@.part
	mkdir -p $@.part
	curl -fsSL https://github.com/projectM-visualizer/projectm/releases/download/v$(PROJECTM)/libprojectM-$(PROJECTM).tar.gz | tar -xz --strip-components=1 -C $@.part
	mv $@.part $@

.tmp/projectm-linux64: .tmp/projectm-src
	rm -rf $@.part .tmp/projectm-build-linux64
	cmake -S .tmp/projectm-src -B .tmp/projectm-build-linux64 $(PROJECTM_CMAKE) -DCMAKE_INSTALL_PREFIX=$(CURDIR)/$@.part
	cmake --build .tmp/projectm-build-linux64 -j $(NPROC)
	cmake --install .tmp/projectm-build-linux64
	mv $@.part $@

.tmp/projectm-static-linux64: .tmp/projectm-src
	rm -rf $@.part .tmp/projectm-build-static-linux64
	cmake -S .tmp/projectm-src -B .tmp/projectm-build-static-linux64 $(PROJECTM_CMAKE) -DCMAKE_INSTALL_PREFIX=$(CURDIR)/$@.part -DBUILD_SHARED_LIBS=OFF
	cmake --build .tmp/projectm-build-static-linux64 -j $(NPROC)
	cmake --install .tmp/projectm-build-static-linux64
	mv $@.part $@

# Static, as FFmpeg is for Android. projectM builds for OpenGL ES 3 there.
.tmp/projectm-android-arm64: .tmp/projectm-src
	rm -rf $@.part .tmp/projectm-build-android
	cmake -S .tmp/projectm-src -B .tmp/projectm-build-android $(PROJECTM_CMAKE) -DCMAKE_INSTALL_PREFIX=$(CURDIR)/$@.part -DBUILD_SHARED_LIBS=OFF \
		-DCMAKE_TOOLCHAIN_FILE=$(NDK)/build/cmake/android.toolchain.cmake -DANDROID_ABI=arm64-v8a -DANDROID_PLATFORM=android-$(ANDROID_API)
	cmake --build .tmp/projectm-build-android -j $(NPROC)
	cmake --install .tmp/projectm-build-android
	mv $@.part $@

# projectM on Windows wants GLEW, shared, as fmw initialises it for projectM.
# GLEW's own build of it needs no C runtime.
.tmp/glew-win64:
	rm -rf $@.part .tmp/glew-$(GLEW)
	mkdir -p $@.part/bin $@.part/lib
	curl -fsSL -o .tmp/glew.zip https://github.com/nigels-com/glew/releases/download/glew-$(GLEW)/glew-$(GLEW)-win32.zip
	unzip -q -d .tmp .tmp/glew.zip
	cp .tmp/glew-$(GLEW)/bin/Release/x64/glew32.dll $@.part/bin/
	cp .tmp/glew-$(GLEW)/lib/Release/x64/glew32.lib $@.part/lib/
	cp -r .tmp/glew-$(GLEW)/include .tmp/glew-$(GLEW)/LICENSE.txt $@.part/
	rm -rf .tmp/glew.zip .tmp/glew-$(GLEW)
	mv $@.part $@

# with the C++ runtime linked in, so only the DLLs themselves get bundled.
# projectM 4.1.6 compiles its playlist library's code without the define
# that exports it, and on Windows its DLL exported nothing.
.tmp/projectm-win64: .tmp/projectm-src .tmp/glew-win64
	rm -rf $@.part .tmp/projectm-build-win64
	cmake -S .tmp/projectm-src -B .tmp/projectm-build-win64 $(PROJECTM_CMAKE) -DCMAKE_INSTALL_PREFIX=$(CURDIR)/$@.part \
		-DCMAKE_SYSTEM_NAME=Windows -DCMAKE_C_COMPILER=$(WINCC) -DCMAKE_CXX_COMPILER=$(WINCXX) -DCMAKE_SHARED_LINKER_FLAGS=-static -DCMAKE_CXX_FLAGS=-DprojectM_playlist_EXPORTS \
		-DGLEW_INCLUDE_DIR=$(CURDIR)/.tmp/glew-win64/include -DGLEW_SHARED_LIBRARY_RELEASE=$(CURDIR)/.tmp/glew-win64/lib/glew32.lib
	cmake --build .tmp/projectm-build-win64 -j $(NPROC)
	cmake --install .tmp/projectm-build-win64
	mv $@.part $@

.tmp/presets:
	rm -rf $@.part
	mkdir -p $@.part
	curl -fsSL $(PRESETS) | tar -xz --strip-components=1 -C $@.part
	mv $@.part $@

.tmp/textures:
	rm -rf $@.part
	mkdir -p $@.part
	curl -fsSL $(TEXTURES) | tar -xz --strip-components=2 -C $@.part --wildcards '*/textures/*'
	mv $@.part $@

# FFmpeg and projectM go in lib/ next to the binary, which finds them through
# its rpath, the presets in presets/ and the textures they use in textures/
linux: .tmp/linux64 .tmp/Vulkan-Headers .tmp/projectm-linux64 .tmp/presets .tmp/textures
	rm -rf dist/fmw
	mkdir -p dist/fmw/lib
	cd cmd/fmw && PKG_CONFIG_PATH=$(CURDIR)/.tmp/linux64/lib/pkgconfig \
		CGO_CFLAGS="$(CGO_CFLAGS) -I$(CURDIR)/.tmp/projectm-linux64/include" \
		CGO_LDFLAGS='-L$(CURDIR)/.tmp/projectm-linux64/lib -Wl,-rpath,$$ORIGIN/lib' \
		fyne build --release --tags projectm -o $(CURDIR)/dist/fmw/fmw
	cp -a $(foreach l,$(FFLIBS),.tmp/linux64/lib/lib$(l).so.*) .tmp/projectm-linux64/lib/libprojectM-4*.so.* dist/fmw/lib/
	cp .tmp/linux64/LICENSE.txt dist/fmw/lib/FFmpeg-LICENSE.txt
	cp .tmp/projectm-src/LICENSE.txt dist/fmw/lib/projectM-LICENSE.txt
	cp -a .tmp/presets dist/fmw/presets
	cp -a .tmp/textures dist/fmw/textures
	tar -cJf dist/fmw_linux_x64.tar.xz -C dist fmw

# One binary with FFmpeg and projectM in it, next to the presets, their
# textures and the licenses of what it bundles. It still takes glibc, OpenGL,
# EGL and the windowing libraries from the system, like any desktop app. Go
# puts CGO_LDFLAGS ahead of milkdrop.go's -l flags, so projectM is named here
# too, before the C++ runtime and OpenGL it needs.
linux-static: .tmp/static-linux64 .tmp/projectm-static-linux64 .tmp/Vulkan-Headers .tmp/presets .tmp/textures
	rm -rf dist/fmw-static
	mkdir -p dist/fmw-static
	cd cmd/fmw && PKG_CONFIG_PATH=$(CURDIR)/.tmp/static-linux64/lib/pkgconfig \
		CGO_CFLAGS="$(CGO_CFLAGS) -I$(CURDIR)/.tmp/projectm-static-linux64/include" \
		CGO_LDFLAGS="-L$(CURDIR)/.tmp/projectm-static-linux64/lib -lprojectM-4-playlist -lprojectM-4 -l:libstdc++.a -static-libgcc -lGL" \
		fyne build --release --tags projectm -o $(CURDIR)/dist/fmw-static/fmw
	cp -a .tmp/static-linux64/licenses dist/fmw-static/licenses
	cp .tmp/projectm-src/LICENSE.txt dist/fmw-static/licenses/projectM-LICENSE.txt
	cp -a .tmp/presets dist/fmw-static/presets
	cp -a .tmp/textures dist/fmw-static/textures

appimage: linux .tmp/appimagetool
	rm -rf .tmp/AppDir
	cp -a dist/fmw .tmp/AppDir
	ln -s fmw .tmp/AppDir/AppRun
	cp logo.png .tmp/AppDir/com.roffe.fmw.png
	ln -s com.roffe.fmw.png .tmp/AppDir/.DirIcon
	printf '$(DESKTOP)Exec=fmw %%F\n' > .tmp/AppDir/com.roffe.fmw.desktop
	ARCH=x86_64 VERSION=$(VERSION) .tmp/appimagetool --appimage-extract-and-run --no-appstream .tmp/AppDir dist/fmw-x86_64.AppImage

# The linux build in $(PREFIX)/lib/fmw, linked from $(PREFIX)/bin, with a
# desktop entry so it shows up in the menu and as a player for the MIMETYPES
install-linux: linux
	rm -rf $(PREFIX)/lib/fmw
	mkdir -p $(PREFIX)/lib $(PREFIX)/bin $(PREFIX)/share/applications
	cp -a dist/fmw $(PREFIX)/lib/fmw
	ln -sf ../lib/fmw/fmw $(PREFIX)/bin/fmw
	install -Dm644 logo.png $(PREFIX)/share/icons/hicolor/512x512/apps/com.roffe.fmw.png
	printf '$(DESKTOP)Exec=$(PREFIX)/bin/fmw %%F\n' > $(PREFIX)/share/applications/com.roffe.fmw.desktop
	-update-desktop-database $(PREFIX)/share/applications
	-kbuildsycoca6

windows: .tmp/win64 .tmp/Vulkan-Headers .tmp/projectm-win64 .tmp/presets .tmp/textures
	rm -rf dist/windows dist/fmw_windows_x64.zip
	mkdir -p dist/windows
	cd cmd/fmw && CGO_ENABLED=1 CC=$(WINCC) PKG_CONFIG_LIBDIR=$(CURDIR)/.tmp/win64/lib/pkgconfig \
		CGO_CFLAGS="$(CGO_CFLAGS) -I$(CURDIR)/.tmp/projectm-win64/include -I$(CURDIR)/.tmp/glew-win64/include" \
		CGO_LDFLAGS="-L$(CURDIR)/.tmp/projectm-win64/lib -L$(CURDIR)/.tmp/glew-win64/lib" \
		fyne package --os windows --release --tags projectm
	mv cmd/fmw/fmw.exe dist/windows/
	cp $(foreach l,$(FFLIBS),.tmp/win64/bin/$(l)-*.dll) .tmp/projectm-win64/bin/*.dll .tmp/glew-win64/bin/glew32.dll dist/windows/
	cp .tmp/win64/LICENSE.txt dist/windows/FFmpeg-LICENSE.txt
	cp .tmp/projectm-src/LICENSE.txt dist/windows/projectM-LICENSE.txt
	cp .tmp/glew-win64/LICENSE.txt dist/windows/GLEW-LICENSE.txt
	cp -a .tmp/presets dist/windows/presets
	cp -a .tmp/textures dist/windows/textures
	cd dist/windows && zip -qr ../fmw_windows_x64.zip *

# No visualizations, fmw makes no GL context for projectM on macOS
macos: .tmp/macos
	rm -rf dist/macos
	mkdir -p dist/macos
	cd cmd/fmw && PKG_CONFIG_PATH=$(CURDIR)/.tmp/macos/lib/pkgconfig fyne build --release -o $(CURDIR)/dist/macos/fmw
	cp .tmp/macos/LICENSE.txt dist/macos/FFmpeg-LICENSE.txt

# Debug signed, as --release needs a keystore, and with go.work's checkouts,
# ahead of what go.mod pins. Video decodes with zero copy where the GPU has
# Vulkan Video, else on MediaCodec. fyne package leaves FyneApp.toml's fyneDo
# migration out of mobile builds, so its tag is given here.
android: GOWORK=
android: .tmp/android-arm64 .tmp/projectm-android-arm64 .tmp/Vulkan-Headers
	mkdir -p dist
	cp cmd/fmw/FyneApp.toml .tmp/FyneApp.toml # fyne package bumps its Build every run
	cd cmd/fmw && ANDROID_NDK_HOME=$(NDK) PKG_CONFIG_LIBDIR=$(CURDIR)/.tmp/android-arm64/lib/pkgconfig \
		CGO_CFLAGS="$(CGO_CFLAGS) -I$(CURDIR)/.tmp/projectm-android-arm64/include" \
		CGO_LDFLAGS="-L$(CURDIR)/.tmp/projectm-android-arm64/lib" \
		fyne package --os android/arm64 --tags migrated_fynedo,projectm
	mv .tmp/FyneApp.toml cmd/fmw/FyneApp.toml
	mv cmd/fmw/fmw.apk dist/fmw_android_arm64.apk

install-android: android
	$(ADB) install -r -d dist/fmw_android_arm64.apk

clean:
	rm -rf .tmp dist

.PHONY: linux appimage windows clean

VERSION=$(shell sed -n 's/^Version = "\(.*\)"/\1/p' cmd/fmw/FyneApp.toml)
FFMPEG=https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n9.0-latest
VULKAN_HEADERS=vulkan-sdk-1.4.363.0
FFLIBS=avformat avcodec avutil swscale swresample
WINCC?=x86_64-w64-mingw32-clang
WINCXX?=x86_64-w64-mingw32-clang++
PROJECTM=4.1.6
GLEW=2.3.1
PRESETS=https://github.com/projectM-visualizer/presets-cream-of-the-crop/archive/0180df21f5e0bd39b9060cc5de420ed2f1f9e509.tar.gz
TEXTURES=https://github.com/projectM-visualizer/presets-milkdrop-texture-pack/archive/6368812f27bc747b517218fbf89d21d59afce4d9.tar.gz
PROJECTM_CMAKE=-DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_LIBDIR=lib -DENABLE_SYSTEM_PROJECTM_EVAL=OFF -DBUILD_TESTING=OFF -DCMAKE_POLICY_VERSION_MINIMUM=3.5

# The releases build against FFmpeg from BtbN and projectM built here, and
# bundle them, not the system's
export GOWORK=off
export CGO_CFLAGS=-O2 -g -I$(CURDIR)/.tmp/Vulkan-Headers/include

default: linux

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
	cmake --build .tmp/projectm-build-linux64 -j $(shell nproc)
	cmake --install .tmp/projectm-build-linux64
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
	cmake --build .tmp/projectm-build-win64 -j $(shell nproc)
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

appimage: linux .tmp/appimagetool
	rm -rf .tmp/AppDir
	cp -a dist/fmw .tmp/AppDir
	ln -s fmw .tmp/AppDir/AppRun
	cp logo.png .tmp/AppDir/com.roffe.fmw.png
	ln -s com.roffe.fmw.png .tmp/AppDir/.DirIcon
	printf '[Desktop Entry]\nType=Application\nName=fmw\nExec=fmw %%f\nIcon=com.roffe.fmw\nCategories=AudioVideo;Video;Player;\n' > .tmp/AppDir/com.roffe.fmw.desktop
	ARCH=x86_64 VERSION=$(VERSION) .tmp/appimagetool --appimage-extract-and-run --no-appstream .tmp/AppDir dist/fmw-x86_64.AppImage

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

clean:
	rm -rf .tmp dist

.PHONY: linux appimage windows clean

VERSION=$(shell sed -n 's/^Version = "\(.*\)"/\1/p' cmd/fmw/FyneApp.toml)
FFMPEG=https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n9.0-latest
VULKAN_HEADERS=vulkan-sdk-1.4.363.0
FFLIBS=avformat avcodec avutil swscale swresample
WINCC?=x86_64-w64-mingw32-clang

# The releases build against FFmpeg from BtbN and bundle it, not the system's
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

# FFmpeg goes in lib/ next to the binary, which finds it through its rpath
linux: .tmp/linux64 .tmp/Vulkan-Headers
	rm -rf dist/fmw
	mkdir -p dist/fmw/lib
	cd cmd/fmw && PKG_CONFIG_PATH=$(CURDIR)/.tmp/linux64/lib/pkgconfig CGO_LDFLAGS='-Wl,-rpath,$$ORIGIN/lib' fyne build --release -o $(CURDIR)/dist/fmw/fmw
	cp -a $(foreach l,$(FFLIBS),.tmp/linux64/lib/lib$(l).so.*) dist/fmw/lib/
	cp .tmp/linux64/LICENSE.txt dist/fmw/lib/FFmpeg-LICENSE.txt
	tar -cJf dist/fmw_linux_x64.tar.xz -C dist fmw

appimage: linux .tmp/appimagetool
	rm -rf .tmp/AppDir
	cp -a dist/fmw .tmp/AppDir
	ln -s fmw .tmp/AppDir/AppRun
	cp logo.png .tmp/AppDir/com.roffe.fmw.png
	ln -s com.roffe.fmw.png .tmp/AppDir/.DirIcon
	printf '[Desktop Entry]\nType=Application\nName=fmw\nExec=fmw %%f\nIcon=com.roffe.fmw\nCategories=AudioVideo;Video;Player;\n' > .tmp/AppDir/com.roffe.fmw.desktop
	ARCH=x86_64 VERSION=$(VERSION) .tmp/appimagetool --appimage-extract-and-run --no-appstream .tmp/AppDir dist/fmw-x86_64.AppImage

windows: .tmp/win64 .tmp/Vulkan-Headers
	rm -rf dist/windows dist/fmw_windows_x64.zip
	mkdir -p dist/windows
	cd cmd/fmw && CGO_ENABLED=1 CC=$(WINCC) PKG_CONFIG_LIBDIR=$(CURDIR)/.tmp/win64/lib/pkgconfig fyne package --os windows --release
	mv cmd/fmw/fmw.exe dist/windows/
	cp $(foreach l,$(FFLIBS),.tmp/win64/bin/$(l)-*.dll) dist/windows/
	cp .tmp/win64/LICENSE.txt dist/windows/FFmpeg-LICENSE.txt
	cd dist/windows && zip -q ../fmw_windows_x64.zip *

clean:
	rm -rf .tmp dist

# Build Directory

The build directory is used to house all the build files and assets for your application. 

The structure is:

* bin - Output directory
* darwin - macOS specific files
* windows - Windows specific files
* linux - Linux specific files (Sirdar's own; see below)

## Mac

The `darwin` directory holds files specific to Mac builds.
These may be customised and used as part of the build. To return these files to the default state, simply delete them
and
build with `wails build`.

The directory contains the following files:

- `Info.plist` - the main plist file used for Mac builds. It is used when building using `wails build`.
- `Info.dev.plist` - same as the main plist file but used when building using `wails dev`.

## Windows

The `windows` directory contains the manifest and rc files used when building with `wails build`.
These may be customised for your application. To return these files to the default state, simply delete them and
build with `wails build`.

- `icon.ico` - The icon used for the application. This is used when building using `wails build`. If you wish to
  use a different icon, simply replace this file with your own. If it is missing, a new `icon.ico` file
  will be created using the `appicon.png` file in the build directory.
- `installer/*` - The files used to create the Windows installer. These are used when building using `wails build`.
- `info.json` - Application details used for Windows builds. The data here will be used by the Windows installer,
  as well as the application itself (right click the exe -> properties -> details)
- `wails.exe.manifest` - The main application manifest file.

## Linux

The `linux` directory is Sirdar's own, not Wails': `wails build` reads
nothing from it and produces a bare `Sirdar` executable. These three files
are what turn that executable into something a person can install, and
`scripts/package-linux.sh` copies them next to it in `bin/` before anything
zips that directory.

- `sirdar.desktop` - the launcher entry. Its `StartupWMClass` must stay equal
  to the `programName` constant in `desktop/main.go`, or the running window
  does not group under the icon that launched it.
- `sirdar.png` - the icon, 512x512, the size a hicolor theme indexes it
  under. `scripts/make-icons.sh` writes it from the same raster as
  `appicon.png`; do not edit it by hand.
- `install.sh` - copies all three into `~/.local`, with no sudo and nothing
  outside `$HOME`. `--uninstall` takes them back out.

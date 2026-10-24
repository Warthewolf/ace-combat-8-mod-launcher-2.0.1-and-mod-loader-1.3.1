# Building and releasing

## Changing the launcher's version

The version is in five places; change them together:

| File | What to change |
|---|---|
| `launcher/main.go` | `appVersion = "2.0.1"` |
| `launcher/winres/app.rc` | `FILEVERSION 2,0,1,0`, `PRODUCTVERSION 2,0,1,0`, `"FileVersion"`, `"ProductVersion"` |
| `launcher/winres/app.manifest` | `version="2.0.1.0"` |
| `launcher/package/*.txt` | The title line of each README and READ ME FIRST |
| `launcher/package/CHANGELOG.txt` | A new section at the top |

Then rebuild the Windows resource file:

```sh
cd launcher/winres
llvm-rc /fo app.res app.rc
llvm-cvtres /machine:x64 /out:../rsrc_windows_amd64.syso app.res
```

## Building

```sh
cd launcher
go test ./...
./build.sh            # or .\build.ps1 on Windows
```

`dist/ACE8ModLauncher.exe` (Windows) and `dist/ACE8ModLauncher` (Linux).

## Packaging

Windows zip `ACE8_Mod_Launcher_<version>_and_Mod_Loader_<loader>.zip`:

```
READ ME FIRST.txt                 package/READ ME FIRST.txt, CRLF line endings
ACE8 Mod Launcher <version>\
  ACE8ModLauncher.exe
  README.txt                      package/README-windows.txt, CRLF
  CHANGELOG.txt                   package/CHANGELOG.txt, CRLF
  Big Shoulders font licence.txt  ui/bigshoulders-OFL.txt
War’s Mod Loader <loader>\
  Game\Binaries\Win64\dwmapi.dll
  Game\Binaries\Win64\ue4ss\UE4SS.dll, UE4SS-settings.ini, LICENSE
  Game\Binaries\Win64\ue4ss\Mods\IoStoreLoaderMod\
    dlls\main.dll, enabled.txt, README.txt, CHANGELOG.txt, source\
```

Linux zip `ACE8_Mod_Launcher_<version>_Linux_and_Mod_Loader_<loader>.zip`:

```
ACE8 Mod Launcher <version> Linux\
  ACE8ModLauncher                 must keep its executable bit (zip it on Linux)
  README.txt                      package/README-linux.txt
  CHANGELOG.txt
  Big Shoulders font licence.txt
War’s Mod Loader <loader>\        the same as in the Windows zip
```

The launcher's **Install** looks for a folder next to its own that has
`Game\Binaries\Win64\dwmapi.dll` and `...\IoStoreLoaderMod\dlls\main.dll`, and uses
the newest Mod Loader it finds, so keep the two folders side by side in the zip.

Don't put `ModLauncher.bat`, `ModLauncher.ps1` or `EasyAntiCheat` files in the
Mod Loader folder; the launcher replaces them.

## Before publishing

- Run the Windows exe once on a real PC: switch both ways, start Modded and check
  Last game start.
- Search the zips for personal paths (your user name, `C:\Users\...`).
- **Antivirus:** each build is a new, unknown file to reputation checks. Submit the
  final exe as a false positive to Microsoft
  (<https://www.microsoft.com/en-us/wdsi/filesubmission>) and Norton
  (<https://submissions.norton.com>) before sharing, and don't rebuild after.
  Signing the exe (for example Microsoft's Artifact Signing) avoids most of this.

## GitHub release

Tag `v<version>` on `main`, attach both zips, and say in the notes which zip is
for which system, that Modded play is offline only, and that the exe isn't signed.

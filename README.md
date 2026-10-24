# ACE8 Mod Launcher 2.0.1 and Mod Loader 1.3.1

Source code for the ACE8 Mod Launcher (Windows and Linux / Steam Deck) and the
AC8 Mod Loader (IoStoreLoaderMod) for Ace Combat 8 on PC.

- **ACE8 Mod Launcher** switches the game between Modded and Unmodded, manages
  mods and their load order, and starts the game (anti-cheat off for Modded,
  offline and single player only).
- **AC8 Mod Loader 1.3.1** is a UE4SS C++ mod that loads `.utoc`/`.ucas` mods and
  `*_P.pak` mods (text, fonts and other files the game keeps in `.pak` files).

Ready-to-use downloads are on the [Releases](https://github.com/Warthewolf/ace-combat-8-mod-launcher-2.0.1-and-mod-loader-1.3.1/releases) page. Players don't
need anything in this repository.

## Documentation

- [Getting started on Windows](docs/getting-started-windows.md)
- [Linux and Steam Deck](docs/linux-and-steam-deck.md)
- [Managing mods](docs/managing-mods.md)
- [Troubleshooting](docs/troubleshooting.md)
- [After a game update](docs/game-updates.md)
- [For mod makers](docs/mod-makers.md)
- [How the launcher works](docs/launcher-internals.md)
- [How the Mod Loader works](docs/mod-loader-internals.md)
- [Building and releasing](docs/releasing.md)

## Layout

| Folder | What's in it |
|---|---|
| `docs/` | The guides above |
| `launcher/` | The launcher: Go program with its window (`ui/`) built in |
| `launcher/package/` | The README, CHANGELOG and READ ME FIRST files shipped with the downloads |
| `launcher/winres/` | Windows icon, version information and manifest (`rsrc_windows_amd64.syso` is built from these) |
| `mod-loader/IoStoreLoaderMod/` | The Mod Loader: README, CHANGELOG and `source/` (C, with tests) |

## Building the launcher

Needs [Go](https://go.dev/dl/) 1.24 or newer. Nothing else: the launcher uses
only Go's standard library.

```sh
cd launcher
./build.sh          # Linux / macOS
.\build.ps1         # Windows PowerShell
```

Both build `dist/ACE8ModLauncher.exe` (Windows) and `dist/ACE8ModLauncher`
(Linux, x86-64, also for the Steam Deck). The Linux version is built with
`-tags linuxapp`.

To test (runs on Linux, macOS or WSL; the tests use stand-ins for Windows):

```sh
cd launcher
go test ./...
```

If you change `winres/app.rc` or `winres/app.manifest`, rebuild the resource file,
for example with LLVM:

```sh
cd launcher/winres
llvm-rc /fo app.res app.rc
llvm-cvtres /machine:x64 /out:../rsrc_windows_amd64.syso app.res
```

## Building the Mod Loader

See the "Source" section of `mod-loader/IoStoreLoaderMod/README.txt`:
`source/src/build-MSVC.ps1` (Windows) or `source/src/build-LLVM.sh`, and the tests
in `source/test`. Builds go to `source/build`. The game loads the result as
`Game/Binaries/Win64/ue4ss/Mods/IoStoreLoaderMod/dlls/main.dll`.

The Mod Loader finds its hook points in `AceCombat8.exe` by byte patterns,
written for game version 1.1.2.0 (see the top of `source/src/loader.c`). After a
game update that changes them, the Mod Loader logs "patterns not found exactly
once" and loads nothing until the patterns are updated.

## Third-party parts

- **UE4SS** (MIT licence) is not in this repository; the downloads include it
  with its licence.
- The launcher's window uses the **Big Shoulders** font, under the SIL Open Font
  License: `launcher/ui/bigshoulders-OFL.txt`.

## Contact

Any issues please contact @voidcallsreaper on Discord.

# How the launcher works

The launcher is one Go program (standard library only) with its window built in.
It runs a small web server on `127.0.0.1` and shows the window in a browser app
window: Microsoft Edge on Windows, a Chromium-family browser or the default
browser on Linux. Everything happens on the player's computer.

## Source files (`launcher/`)

| File | What it does |
|---|---|
| `main.go` | Start-up: data folder, single instance, the server, opening the window |
| `server.go` | The local API, state sent to the window, support report |
| `presence.go` | Notices when the last window closes; the busy lock that serialises file changes |
| `game.go` | Finding the game (Steam libraries, `libraryfolders.vdf`, `appmanifest`), storage paths, a reader for Valve's `.vdf` files |
| `park.go` | The Modded / Unmodded switch: moving files, the journal, undo, held-open folders |
| `storage.go` | Choosing and moving the mod storage folder |
| `install.go` | Installing UE4SS and the Mod Loader; adding mods from zips and folders; removing and restoring |
| `mods.go` | Finding pak mods the way the Mod Loader does; `loadorder.txt` |
| `utoc.go` | Reads `.utoc` file lists, to show which mods change the same game files |
| `scripts.go` | UE4SS script mods and `mods.txt` |
| `runlog.go` | Reads `AC8IoStoreLoader.log` into "Last game start" |
| `launch.go` | Starting the game |
| `settings.go` | `settings.json` |
| `os_windows.go`, `filedialog_windows.go` | Windows: Edge window, Explorer, process list, registry, native folder dialogs (COM), elevation |
| `os_linux.go` | Linux (`-tags linuxapp`): browser window, `xdg-open`, `kdialog`/`zenity`, `/proc`, app menu entry |
| `linux_common.go` | Linux logic that tests can run anywhere: the Steam launch script, editing `localconfig.vdf` |
| `linux_none_windows.go` | Empty stand-ins so the Windows exe contains none of the Linux parts |
| `os_other.go` | Stand-ins for tests: records what the real program would do into files |
| `ui/` | The window: `index.html`, `app.css`, `app.js`, images, the Big Shoulders font |
| `winres/`, `rsrc_windows_amd64.syso` | Windows icon, version information and manifest |

Build tags: Windows builds use `os_windows.go`; the Linux release uses
`os_linux.go` (`-tags linuxapp`); a plain `go test` on Linux or macOS uses
`os_other.go`, so tests behave like the Windows version without touching the
system.

## Start-up and the single instance

1. The data folder is `%LOCALAPPDATA%\ACE8 Mod Launcher` (Windows) or
   `~/.local/share/ace8-mod-launcher` (Linux). It holds `settings.json`,
   `launcher.log` and `instance.json`.
2. If `instance.json` points to a launcher that answers, and it is the **same
   build** (version plus a hash of the exe), that one's window is shown and the
   new start ends. A **different build** is asked to quit (`/api/quit`, or the
   process is stopped for launchers before 2.0.1) and the new one takes over,
   unless the old one is in the middle of a switch or install.
3. The server listens on a random free port; a random token is put in the window's
   URL.
4. When no window has been heard from for a while (each window pings every 20 s
   and says "bye" when it closes), the launcher exits. It never exits during a
   switch or install.

## The local API

All calls are `GET`/`POST` to `/api/<name>` with the header `X-Token`. Requests
with another `Host` header are refused, so web pages can't reach it.

| Call | What it does |
|---|---|
| `state` | Everything the window shows: game, mode, setup, mods, last run |
| `status` | The quick part, polled every 2 s: game running, busy, a fingerprint of the folders |
| `switch` `{to}` | Modded / Unmodded |
| `play` `{force}` | Start the game |
| `setup` | Install or update UE4SS and the Mod Loader |
| `add` `{paths, upload, pick}`, `upload`, `pick-mods` | Add mods |
| `order` `{mods}` | Save the load order |
| `script` `{name, enabled}` | Switch a script mod |
| `remove`, `restore`, `delete-removed` | Removed mods |
| `game`, `pick-game` | Game folder |
| `storage`, `pick-storage` | Mod storage folder |
| `settings` | Extra launch options, welcome shown |
| `steam-launch-options` | Linux: put the launch script into Steam's settings |
| `reveal`, `open` | Show a folder; open a link |
| `loader-log`, `report` | The Mod Loader's log; the support report |
| `hello`, `ping`, `bye`, `quit` | Single instance and window presence |

Actions that change files take the busy lock, so two never run at once.

## Modded and Unmodded

Four items move between the game folder and `<storage>\Switched off`, keeping
their relative paths:

```
Game\Binaries\Win64\dwmapi.dll
Game\Binaries\Win64\ue4ss
Game\Content\Paks\~mods
Game\Content\Paks\LogicMods
```

The mode is worked out from where the items are, not stored: all in the game is
Modded, all in storage is Unmodded, some in each is "mixed" (a switch that didn't
finish), none anywhere is "none".

A switch:

1. Refuses while the game is running.
2. Writes `switch-journal.json` in the storage folder, then moves each item,
   adding every finished step to the journal.
3. If a step fails, it moves the finished steps back in reverse order; when that
   works the journal is marked finished.
4. A folder another program has open can't be renamed on Windows, so the move
   falls back to moving the folder's contents piece by piece.
5. Across drives, files are copied (keeping their times), the original is renamed
   aside, then deleted.

## The storage folder

Default: `<game folder> - mod storage` next to the game, on the same drive, so
switching is a rename. A chosen folder is kept per game in `settings.json`
(`storageDir`, `storageGame`). Moving it carries `Switched off`, `Removed mods`
and the journal along, and rewrites the paths in `removed.json` files. Actions
that need it are refused while a chosen folder can't be found (a drive not
connected).

## Adding mods

`install.go` reads a zip or a folder into a list of files, then sorts each file
into a unit:

- Paths inside `ue4ss\Mods\<name>\`, `~mods\`, `LogicMods\` keep their place.
- A folder with `Scripts\main.lua` or `dlls\main.dll` is a script mod.
- Folders with `.utoc`/`_P.pak` files become pak mods, using the Mod Loader's own
  rules (a `_P.utoc` with a same-named `.pak` is part of the `.pak`, not a mod of
  its own).
- Several candidate folders that look like versions of one mod → the window asks.

Each unit is first written to a staging folder, then committed. An older copy of
the same mod is moved to Removed mods first; if a commit fails, it is put back.
Names are cleaned so Windows can store them (reserved names, trailing dots and
spaces). While Unmodded, mods go to the matching place under `Switched off`.

## Reading the Mod Loader's log

`runlog.go` reads the lines described in
[Troubleshooting](troubleshooting.md#what-the-mod-loaders-log-lines-mean). Per mod
it counts `result code=` (a `.utoc`) and `Legacy result AL= pak-in-table=` (a
`.pak`; in the table counts as loaded). `testdata/` has real logs from 1.1.0 and
1.3.1 the tests check against.

## Starting the game

**Windows, Modded:** make sure Steam is running and signed in (the registry's
`ActiveUser`), then start `AceCombat8.exe -eac-nop-loaded -nosplash` from
`Game\Binaries\Win64` with `SteamAppId`/`SteamGameId=2288340`. If Windows answers
"requires elevation" (the exe is set to run as administrator), start it through
`ShellExecuteEx` and write `steam_appid.txt`, since the environment doesn't pass
through.

**Windows, Unmodded:** `steam://rungameid/2288340`.

**Linux, both modes:** `steam://rungameid/2288340`. The script in Steam's launch
options (`ace8-steam-launch.sh`, written by the launcher at each start) decides:
if `dwmapi.dll` is in `Game/Binaries/Win64`, it replaces `start_protected_game.exe`
with `AceCombat8.exe -eac-nop-loaded -nosplash` and adds `dwmapi=n,b` to
`WINEDLLOVERRIDES`; otherwise it runs Steam's command unchanged.

## Tests

`go test ./...` (about a minute) runs every feature against fake game folders:
switching (including held-open folders and other drives), storage moves, adding
mods from real-world layouts, load order, log reading, setup, playing, and the
Linux script run in front of a fake Steam command.

Two optional checks compare the launcher's mod discovery with the real Mod Loader's
dry-run builds: set `ACE8_LOADER_SELFTEST` (1.1.0) and `ACE8_LOADER131_SELFTEST`
(1.3.1) to their test executables.

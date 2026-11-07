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
| `update_merge.go` | Updating script mods and UE4SS's `shared` folder without losing the player's files (`ace8-keep.txt`) |
| `moddates.go` | When each mod was added or last updated (`mod-dates.json`) |
| `mods.go` | Finding pak mods the way the Mod Loader does; `loadorder.txt` |
| `utoc.go` | Reads `.utoc` file lists, to show which mods change the same game files |
| `scripts.go` | UE4SS script mods and `mods.txt` |
| `runlog.go` | Reads `AC8IoStoreLoader.log` into "Last game start" |
| `ue4sslog.go` | Reads `ue4ss\UE4SS.log` for the script mods UE4SS started, for "Last game start" |
| `launch.go` | Starting the game |
| `settings.go` | `settings.json`: game folder, extra launch options, mod storage folder, whether the first-run tour was shown |
| `os_windows.go`, `filedialog_windows.go` | Windows: Edge window, Explorer, process list, registry, native folder dialogs (COM), elevation |
| `os_linux.go` | Linux (`-tags linuxapp`): browser window, `xdg-open`, `kdialog`/`zenity`, `/proc`, app menu entry |
| `linux_common.go` | Linux logic that tests can run anywhere: the Steam launch script, editing `localconfig.vdf` |
| `linux_none_windows.go` | Empty stand-ins so the Windows exe contains none of the Linux parts |
| `os_other.go` | Stand-ins for tests: records what the real program would do into files |
| `ui/` | The window: `index.html`, `app.css`, `app.js`, images, the Big Shoulders font (see [The window](#the-window)) |
| `winres/`, `rsrc_windows_amd64.syso` | Windows icon, version information and manifest |

Build tags: Windows builds use `os_windows.go`; the Linux release uses
`os_linux.go` (`-tags linuxapp`); a plain `go test` on Linux or macOS uses
`os_other.go`, so tests behave like the Windows version without touching the
system.

## Start-up and the single instance

1. The data folder is `%LOCALAPPDATA%\ACE8 Mod Launcher` (Windows) or
   `~/.local/share/ace8-mod-launcher` (Linux). It holds `settings.json`,
   `launcher.log` and `instance.json`.
2. A lock makes sure only one launcher goes on: a named mutex
   (`Local\ACE8ModLauncher`) on Windows, `flock` on `instance.lock` in the data
   folder on Linux. A second start that can't get it waits up to 8 s for the
   first one's window and shows it.
3. If `instance.json` points to a launcher that answers, and it is the **same
   build** (version plus a hash of the exe), that one's window is shown and the
   new start ends. A **different build** is asked to quit (`/api/quit`) and the
   new one takes over. A launcher that is busy (a switch or an install) says no
   and is left alone; only launchers from before 2.0.1, which can't be asked,
   are stopped.
4. The server listens on a random free port; a random token is put in the window's
   URL. On Linux the data folder is private to the user (0700) and
   `instance.json` is 0600.
5. Once the game is known, leftovers of an update that was cut short
   (`<name>.ace8-updating`) are tidied up, as they are before each Play and switch.
6. When no window has been heard from for a while (each window pings every 20 s
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
| `settings` `{extraArgs, welcomeDone}` | Extra launch options; first-run tour shown (`welcomeDone`) |
| `steam-launch-options` | Linux: put the launch script into Steam's settings |
| `reveal`, `open` | Show a folder; open a link |
| `loader-log`, `ue4ss-log`, `report` | The Mod Loader's log; UE4SS's log (the last 400 lines); the support report |
| `hello`, `ping`, `bye`, `quit` | Single instance and window presence |

Actions that change files take the busy lock, so two never run at once.
`settings` doesn't: it changes no mod files, so the window's settings save even
while a switch runs. `settings.json` is written to a temporary file and renamed;
the settings in memory only change once that worked.

`upload` streams a drop straight into `uploads\<id>` in the data folder (no
second copy in the temp folder), refuses more than 16 GB, stops as soon as the
game's own files (`global.utoc`) arrive, and removes what it saved if anything
fails. Leftover uploads are cleared at start-up and after 6 hours.

## The window

`ui/index.html`, `app.css` and `app.js` are built into the exe and use nothing
from the network: no framework, no CDN. `app.js` loads `state` once, polls
`status` every 2 s (8 s while the window is hidden), and loads `state` again when
the fingerprint, the running game, the log or the busy state changes. Actions
answer with the new state, and the window redraws from it.

Four pages: **Play**, **Mods**, **Settings** and **Help**, with a status bar along
the top (Game, Mode, Mod Loader, Mods, Last game start) whose cells go to the
matching page. Text for one system only is marked `data-os="windows"` or
`data-os="linux"`; `applyOS()` picks by `state.platform`.

The window keeps a few things of its own in the browser's `localStorage`: the
colours (`ace8-theme`), the last page (`ace8ml-page`), a hidden Steam tip
(`ace8ml-hide-steamtip`) and a hidden First steps list
(`ace8ml-hide-firststeps`). Everything else comes from the launcher.

### The first-run tour

A short walk through the window, one step at a time (`TOUR` in `app.js`). It
starts by itself once, the first time the game is found (`settings.welcomeDone`
is false). It waits while the storage folder is missing, a switch is half
finished or a dialog is open, since those need the player first.

| Step | Page | Points at |
|---|---|---|
| Modded or Unmodded | Play | The mode switch |
| Set up modding (once) | Play | The First steps list's first step, or the Set up modding card. Only while modding isn't set up; with the install files next to the launcher it has a **Set up now** button |
| Add a mod | Mods | **Add a mod…** |
| Switch mods on or off | Mods | The first mod, or the empty list |
| Press Play | Play | The Play button; on Linux without the launcher's line in Steam, the One step in Steam card |
| If something goes wrong | Settings | **Copy support report** |

The steps and their text follow the live state, so setting up modding during the
tour drops that step and the count changes with it. Each step goes to its page,
dims the rest of the window with a hole over its target (which stays usable),
and puts the card where it covers least of it. Keyboard focus stays in the card;
the arrow keys page through the steps.

**Done**, **Skip the tour** and Esc close it and post `settings`
`{welcomeDone: true}` without a "Saved." message. It doesn't start again in that
window either (`tourSeen`), even if the save fails. **Show the tour again**
(Help, Quick start) and **Show the tour** (bottom of the menu) open it at any
time once the game has been found.

### First steps

Once the tour has been seen, the Play page shows a First steps list: Set up
modding, Add a mod, Play Modded. Each ticks itself from the state: UE4SS and the
Mod Loader installed; at least one mod; a Mod Loader log from a game start
(`state.run`). Each step has its own button, and the next step's is the bright
one. The list stays until all three are done or the player presses **Hide**
(`ace8ml-hide-firststeps`), and meanwhile stands in for the Set up modding card,
so the page has one next thing to do. Help's Quick start has the same steps.

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
2. Deals with a switch that stopped part way (the launcher was closed, the PC
   went off): going the same way, it carries on from `switch-journal.json`; going
   the other way, it puts that switch's steps back first. A journal whose paths
   are outside this game and its storage folder is kept aside as
   `switch-journal (other folder).json` and not followed.
3. Writes each step to the journal as pending before it moves the item, and
   marks it done after. The journal is written to a temporary file and renamed.
4. When an item is on both sides (a mod put into `~mods` by hand while
   Unmodded, say), the two are **merged**, never one set aside whole. For a file
   that is on both sides, the newer one stays (as the Mod Loader also picks the
   newest of two same-named mods) and the other is kept in the item's
   `(older copy …)` folder in the storage folder, never inside the game. The
   `.utoc`, `.ucas` and `.pak` of one mod decide together. `dwmapi.dll` is a
   single file: the one being switched in wins and the other is kept as
   `dwmapi (older copy …).dll`.
5. If a step fails, it moves the finished steps back in reverse order, taking each
   off the journal as soon as it is back, and stops at the first that can't be
   moved, so the journal lists exactly what is left for the next switch.
6. A folder another program has open can't be renamed on Windows, so the move
   falls back to moving the folder's contents piece by piece.
7. Across drives, the copy is made in a temporary `.ace8-copying` folder and gets
   its real name only when complete; then the original is renamed into
   `.ace8-deleting` in one step and deleted. Both temporary folders sit next to
   `Paks` or `ue4ss`, not inside them, so nothing half copied or half deleted is
   ever loaded, and leftovers are swept at the next switch.

On Linux, `~mods` and `LogicMods` are also found with other capital letters
(`~Mods`), as the game under Proton would find them.

## The storage folder

Default: `<game folder> - mod storage` next to the game, on the same drive, so
switching is a rename. A chosen folder is kept per game in `settings.json`
(`storageDir`, `storageGame`). Moving it carries `Switched off`, `Removed mods`
and the journal along, and rewrites the paths in `removed.json` files once the
new settings are saved. Across drives, everything is copied first and the
originals are deleted only after the settings are saved; a
`.ace8-moving-here.json` note in the new folder lets a move that was cut short
simply be tried again.

Actions that need it are refused while a chosen folder can't be found (a drive
not connected). Choosing a folder that holds the mods (the same folder under a
new drive letter) adopts it. Choosing **Use the default** or an empty folder
instead says where the mods are and goes on without them only when the player
presses the same button again.

When Steam moves the game to another library, the launcher finds it again and
keeps the storage folder: a chosen folder stays chosen, and the old default
`<old game folder> - mod storage` is kept as the chosen folder if it holds mods.
A game folder on a drive that isn't connected is not replaced by another copy of
the game on its own: the window offers that copy (`game.elsewhere`).

## Adding mods

`install.go` reads a zip or a folder into a list of files, then sorts each file
into a unit:

- Paths inside `ue4ss\Mods\<name>\`, `~mods\`, `LogicMods\` keep their place.
- A folder with `Scripts\main.lua` or `dlls\main.dll` is a script mod.
- Folders with `.utoc`/`_P.pak` files become pak mods, using the Mod Loader's own
  rules (a `_P.utoc` with a same-named `.pak` is part of the `.pak`, not a mod of
  its own).
- Several candidate folders that look like versions of one mod → the window asks.
  A picked version is named after the download too ("Su-57 Liveries - Red").
  When several downloads are added together, all of their questions are asked
  first and nothing is added before the player has chosen; each choice's id is
  `<download>|<id>`, so the picks of two downloads can't be mixed up.
- The game's own files (`global.utoc`, or containers that match the top of the
  game's `Paks` folder by name and size) are refused: that is the game's folder,
  not a mod.

**Update or new mod?** An add counts as an update of an installed mod only when
it is plausibly the same mod: it shares at least one `.utoc`/`.ucas`/`.pak` file
name with it (or the installed folder is empty). A folder name alone never
decides, so a different mod that happens to be called "New mod" or "Paks" is
added as "Name (2)". An update whose folder has a new name ("Hornet v1" →
"Hornet v2") keeps the old one's place in the load order, its on/off state and
its first-added date. Script mods match by name ("CoolCam (1).zip" and
"CoolCam-1.3.0" update "CoolCam").

Each unit is first written to a staging folder, then committed. An update
replaces the installed copy in place: the old copy is renamed aside
(`<name>.ace8-updating`), the new one moved in, then the old one deleted; if the
new one can't go in, the old one is renamed back. When the installed folder
holds more than this mod (a `~mods` folder with other mods in it, a pack of
skins), only the matching mod's files are replaced and the rest stays. Leftovers
from a launcher closed mid-update are cleaned up at the next start, add, Play or
switch, preferring a complete old copy.

Problems that stop an added mod from loading (a missing `.ucas`, a `.pak` mod
with a Mod Loader older than 1.3.1, LogicMods without BPModLoaderMod) come back
as `warnings`, separate from `notes`, and the window shows them until closed. Script mods first get the installed
copy's extra files (and those named in `ace8-keep.txt`) copied into the staged
new copy; UE4SS's `shared` folder is updated file by file. The date of each add
or update is kept in `mod-dates.json` in the data folder. Names are cleaned so Windows can store
them (reserved names, trailing dots and spaces). While Unmodded, mods go to the
matching place under `Switched off`.

## Reading the Mod Loader's log

`runlog.go` reads the lines described in
[Troubleshooting](troubleshooting.md#what-the-mod-loaders-log-lines-mean). Per mod
it counts `result code=` (a `.utoc`) and `Legacy result AL= pak-in-table=` (a
`.pak`; in the table counts as loaded). It also reads every `skipped …` line and
maps it to its mod: a `.pak` in `~mods` that the game loaded by itself counts as
loaded, and every other skipped file counts as not loaded, with its reason in
plain words in `run.problems`. "N of M loaded" is counted from these per-file
results, not from the log's own `done:` line. `testdata/` has real logs from
1.1.0 and 1.3.1 the tests check against.

`utoc.go` reads `.utoc` files a stranger made, so every size and offset is
checked against the file before use, the directory index is walked without
recursion and each folder at most once, and the total work is capped: a damaged
or hostile `.utoc` can't hang, crash or fill the memory.

## Starting the game

**Windows, Modded:** make sure Steam is running and signed in (the registry's
`ActiveUser`), then start `AceCombat8.exe -eac-nop-loaded -nosplash` from
`Game\Binaries\Win64` with `SteamAppId`/`SteamGameId=2288340`. If Windows answers
"requires elevation" (the exe is set to run as administrator), start it through
`ShellExecuteEx` and write `steam_appid.txt`, since the environment doesn't pass
through.

If Steam had to be started and its registry still showed someone signed in
(possibly left from a crash), the launcher also waits up to 30 s for Steam's
`steamwebhelper.exe` before starting the game.

**Windows, Unmodded:** `steam://rungameid/2288340`.

**Linux, both modes:** `steam://rungameid/2288340`. The script in Steam's launch
options (`ace8-steam-launch.sh`, written by the launcher at each start) decides:
if `dwmapi.dll` is in `Game/Binaries/Win64`, it replaces `start_protected_game.exe`
with `AceCombat8.exe -eac-nop-loaded -nosplash` and adds `dwmapi=n,b` to
`WINEDLLOVERRIDES`; otherwise it runs Steam's command unchanged. When more than
one Steam folder exists (an old native Steam next to Flatpak Steam), the one
whose libraries hold the game, then the one used most recently, is the one read
and edited.

After Play, the launcher watches for the game for up to 15 minutes
(`launch.waiting`), every 0.5 s at first and every 2 s after 90 s. If the game
never appears, `launch.gaveUp` tells the window it didn't start.

## Tests

`go test ./...` (a little over a minute) runs every feature against fake game
folders: switching (including held-open folders, other drives and switches cut
short), storage moves, adding and updating mods from real-world layouts, load
order, log reading, setup, playing, uploads, and the Linux script run in front of
a fake Steam command. The `fix_*_test.go` files hold a regression test for each
bug fixed in 2.1.0; `utoc.go` also has a fuzz test (`go test -fuzz FuzzParseUtoc`).

Two optional checks compare the launcher's mod discovery with the real Mod Loader's
dry-run builds: set `ACE8_LOADER_SELFTEST` (1.1.0) and `ACE8_LOADER131_SELFTEST`
(1.3.1) to their test executables.

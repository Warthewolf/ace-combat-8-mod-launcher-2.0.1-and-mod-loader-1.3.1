# Troubleshooting

Start with **Settings → Copy support report** and paste it into your message to
@voidcallsreaper on Discord. It has your setup, mods and the latest logs, with
your user name left out.

## The game doesn't start, or crashes, while Modded

1. Try **Unmodded**. If that also fails, it's the game or Steam, not mods.
2. Look at **Last game start** on the Play page.
3. Switch mods off in halves on the Mods page until the game starts; the last half
   you switched off holds the problem mod.
4. Check `Game\Binaries\Win64\ue4ss\UE4SS.log` exists. If not, UE4SS never
   started: check `dwmapi.dll` is next to `AceCombat8.exe`.

## Mods don't show in the game

| Last game start says | What to do |
|---|---|
| Not loaded: "Added after that start" | Start the game again |
| Not loaded, another mod changes the same file | Move the one you want lower in the list |
| "The .ucas file is missing" | Copy the `.ucas` next to the `.utoc` |
| "Game version not supported" | The game was updated: see [After a game update](game-updates.md) |
| "Two mod loaders installed" | Delete the other loader's folder under `ue4ss\Mods` |
| Every pak mod Not loaded with Mod Loader 1.3.1 | Your launcher is older than 2.0.1: update it |

Also: on Windows, a Modded game started with **Steam's** Play button has anti-cheat
on and no mods. Use the launcher's Play.

## What the Mod Loader's log lines mean

`ue4ss\Mods\IoStoreLoaderMod\AC8IoStoreLoader.log` is written each time the game
starts while Modded.

| Line | Meaning |
|---|---|
| `done: 7 of 7 attempted mount requests returned success` | All good |
| `mounting [Name] … result code=0 message=OK` | A `.utoc` mod loaded |
| `mounting Legacy Pak [Name] …` then `Legacy result AL=1 pak-in-table=1` | A `_P.pak` mod (or `.pak`+`.utoc`+`.ucas` set) loaded |
| `AL=0 pak-in-table=1` | The game took the `.pak` but reported a problem; check the mod |
| `patterns not found exactly once` | The game version changed; the Mod Loader does nothing until it is updated |
| `already hooked by: <file>` | Another mod loader is installed |
| `skipped …: its .ucas file is missing` | Copy the `.ucas` next to the `.utoc` |
| `skipped …: same name as …` | The same mod is installed twice |
| `skipped Pak …: already present in the native mount table` | The game loaded that `.pak` itself (normal for `~mods`) |
| `Legacy Pak active mounting unavailable` | `.pak` loading couldn't be set up for this game version |
| Log stops at `hook installed, waiting…` | The game loaded its files before UE4SS started the loader; start again |
| No log at all | UE4SS didn't start the loader: check `enabled.txt` and `dlls\main.dll` in IoStoreLoaderMod |

## A switch says a program has a file open (Windows)

Close the game, Explorer windows showing the game's folders, editors (VS Code,
Notepad++) and command windows, then switch again. The launcher moves what it can
around a folder that is held open, so this is rare. If a switch stops halfway,
the Play page shows **Finish switching** with buttons for either mode.

## The launcher shows old information or an old version

Look at the version under the menu. If it's older than the exe you started, the
old launcher was still open. Since 2.0.1 a newer launcher closes an older one by
itself; for older versions, close the launcher window and start the new one.

## Antivirus warnings

The launcher is new and not code-signed. It starts no scripts or hidden programs
on Windows, but reputation and machine-learning checks still flag new unsigned
programs:

- **Norton** `FileRepMalware`: Security → Security History → Quarantine → the
  item → **Create exception & restore**. Exclusions are under Security → Scans →
  Open → Exclusions.
- **Microsoft Defender** `Trojan:Win32/Wacatac.B!ml`: Windows Security → Virus &
  threat protection → Protection history → the item → Actions → **Restore**; then
  add an exclusion for the launcher's folder.

For everyone: report the exact released exe as a false positive
(Microsoft: <https://www.microsoft.com/en-us/wdsi/filesubmission>, Norton:
<https://submissions.norton.com>). Each rebuild is a new file and starts again, so
submit the final release. Code-signing the exe is the lasting fix.

## Administrator prompt every time (Windows)

If `AceCombat8.exe` is set to run as administrator, Windows asks for permission
on each Modded start. Right-click `AceCombat8.exe` → Properties → Compatibility →
untick **Run this program as an administrator** (also under **Change settings
for all users**).

## Microsoft Visual C++ runtime

UE4SS needs the Visual C++ 2015–2022 runtime (x64). The launcher tells you if it's
missing and links to Microsoft's download.

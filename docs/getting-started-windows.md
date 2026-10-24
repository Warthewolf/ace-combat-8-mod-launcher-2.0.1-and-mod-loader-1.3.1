# Getting started on Windows

## What you need

- Ace Combat 8 on Steam (game version 1.1.2.0 for Mod Loader 1.3.1).
- The download `ACE8_Mod_Launcher_2.0.1_and_Mod_Loader_1.3.1.zip` from the
  [Releases](https://github.com/Warthewolf/ace-combat-8-mod-launcher-2.0.1-and-mod-loader-1.3.1/releases) page.

## Install

1. Unzip the download anywhere, for example in Documents. It has three things:
   - `ACE8 Mod Launcher 2.0.1` — the launcher.
   - `War’s Mod Loader 1.3.1` — UE4SS and the Mod Loader, laid out like the game folder.
   - `READ ME FIRST.txt`.
2. Put UE4SS and the Mod Loader into the game, either way:
   - **By hand:** copy the `Game` folder from `War’s Mod Loader 1.3.1` into
     `...\steamapps\common\ACE COMBAT 8\` and let Windows merge the folders.
   - **With the launcher:** start it (next step) and press **Install**. It uses
     the `War’s Mod Loader 1.3.1` folder sitting next to its own folder.
3. Double-click `ACE8ModLauncher.exe`. The first time, Windows may say
   "Windows protected your PC": press **More info**, then **Run anyway**.

When it's set up, `Game\Binaries\Win64` in the game folder looks like this:

```
Win64\
  AceCombat8.exe
  dwmapi.dll          <- starts UE4SS
  ue4ss\              <- UE4SS, the Mod Loader (ue4ss\Mods\IoStoreLoaderMod) and script mods
  ...the game's own files
```

The launcher finds the game through Steam. If it can't, it asks you to choose
the `ACE COMBAT 8` folder.

## Modded and Unmodded

The Play page has a switch:

| | Modded | Unmodded |
|---|---|---|
| What's in the game | UE4SS, the Mod Loader and your mods | Nothing extra: the game as Steam installed it |
| Play starts | `AceCombat8.exe` directly with anti-cheat off | The game through Steam with anti-cheat on |
| Online | No: offline and single player only | Yes |

Switching moves files; nothing is deleted. While Unmodded, everything waits in
the **mod storage folder** (by default `ACE COMBAT 8 - mod storage`, next to the
game folder, under `Switched off`). If a switch can't finish, everything is put
back the way it was.

What gets moved:

```
Game\Binaries\Win64\dwmapi.dll
Game\Binaries\Win64\ue4ss
Game\Content\Paks\~mods
Game\Content\Paks\LogicMods
```

## Playing

- Always use the launcher's **Play** for Modded. Steam's own Play button turns
  anti-cheat on, which stops UE4SS and the mods.
- Steam has to be running and signed in; the launcher opens it if needed and
  waits for it to sign in.
- You don't need Steam launch options, `AC8_Offline.json` or `ac8noeac.bat` any
  more. If Steam's launch options still have the old anti-cheat line, the
  launcher tells you, so Unmodded can start with anti-cheat on.

After a Modded start, **Last game start** on the Play page lists each mod as
Loaded, Not loaded or Failed, with the reason.

## Settings worth knowing

- **Mod storage folder:** choose where switched-off and removed mods are kept.
  On the same drive as the game, switching is instant; on another drive, each
  switch copies the files.
- **Extra launch options for Modded:** for example `-dx11`.
- **Copy support report:** your setup, mods and the latest logs, ready to paste
  into a message. Your Windows user name is left out.

## Updating the launcher

Close it, replace `ACE8ModLauncher.exe` with the new one, start it. Settings,
mods and load order stay. Since 2.0.1, starting a new version while an older one
is still open closes the older one.

## Antivirus

The launcher is new and not code-signed, so some antivirus programs (Norton,
Microsoft Defender) may flag it the first time; Defender's
`Trojan:Win32/Wacatac.B!ml` is a machine-learning guess, not a known threat.
Restore it from quarantine and add an exclusion. See
[Troubleshooting](troubleshooting.md#antivirus-warnings).

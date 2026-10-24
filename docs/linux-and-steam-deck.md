# Linux and Steam Deck

The Linux launcher (`ACE8_Mod_Launcher_2.0.1_Linux_and_Mod_Loader_1.3.1.zip` on
the [Releases](https://github.com/Warthewolf/ace-combat-8-mod-launcher-2.0.1-and-mod-loader-1.3.1/releases) page) does the same as the Windows one: switch between
Modded and Unmodded, manage mods, start the game. UE4SS and the Mod Loader are
the same Windows files; the game runs them through Proton.

> Status: the launcher, the Steam launch line and UE4SS loading under Proton
> have been confirmed on a Linux PC. Starting Modded still crashed the game early
> in that first test; the cause is being narrowed down (see
> [Known issues](#known-issues)).

## Install

On the Steam Deck, do this in Desktop Mode.

1. Unzip the download, for example into your home folder.
2. Double-click `ACE8ModLauncher` in the `ACE8 Mod Launcher 2.0.1 Linux` folder.
   If your file manager asks, choose **Execute** / **Run**. If it opens as text or
   nothing happens, right-click it → Properties → Permissions → tick
   **Is executable**, or in a terminal:
   ```sh
   chmod +x ACE8ModLauncher && ./ACE8ModLauncher
   ```
3. Press **Install** (it uses the `War’s Mod Loader 1.3.1` folder next to the
   launcher's folder), or copy that folder's `Game` folder into the game folder
   yourself.
4. Add the launcher's line to Steam's launch options (next section). Once only.

After the first start the launcher is in your app menu under Games, and can be
added to Steam as a non-Steam game.

## The Steam launch options line

On Linux, Steam has to start the game (through Proton), so the launcher can't
start `AceCombat8.exe` itself. Instead, a small script goes in front of Steam's
own command. In Steam: right-click **ACE COMBAT 8** → **Properties** →
**General** → **Launch Options**:

```
/home/<you>/.local/share/ace8-mod-launcher/ace8-steam-launch.sh %command%
```

The launcher shows the exact line in **Settings → Starting the game**, with a
**Copy the line** button, and **Set it for me**, which edits Steam's settings
directly while Steam is closed (Steam menu → Exit). It keeps your other launch
options and saves a backup of Steam's settings file (`localconfig.vdf.ace8-backup`).

Each time Steam starts the game, the script checks the game folder:

| Game state | What the script does |
|---|---|
| Modded (`dwmapi.dll` is in `Game/Binaries/Win64`) | Replaces `start_protected_game.exe` with `AceCombat8.exe -eac-nop-loaded -nosplash` and sets `WINEDLLOVERRIDES="dwmapi=n,b"` so Proton loads UE4SS |
| Unmodded | Runs Steam's command unchanged: anti-cheat on |

So the line can stay in Steam for good. Steam's own Play button, and Game Mode on
the Steam Deck, start the game the right way for the mode you chose. Each start is
noted in `steam-launch.log` next to the script; the launcher shows the last one
as "Last start from Steam".

Other launch options can stay; put the launcher's line just before `%command%`:

```
gamemoderun /home/<you>/.local/share/ace8-mod-launcher/ace8-steam-launch.sh %command% -dx11
```

With **Flatpak Steam**, the script goes in
`~/.var/app/com.valvesoftware.Steam/ace8-mod-launcher/` instead (Flatpak Steam
can't see the rest of your home folder). Snap Steam: `~/snap/steam/common/ace8-mod-launcher/`.
The launcher works this out.

## Steam Deck

1. Desktop Mode: set things up as above, choose Modded or Unmodded in the launcher.
2. Game Mode: play ACE COMBAT 8 as usual. The launch line does the rest.

## Where things are

| What | Where |
|---|---|
| Settings, `launcher.log`, the launch script and `steam-launch.log` | `~/.local/share/ace8-mod-launcher/` |
| App menu entry | `~/.local/share/applications/ace8-mod-launcher.desktop` |
| Mod storage | `ACE COMBAT 8 - mod storage` next to the game folder (also on an SD card) |

## The window and folder choosers

- The window opens as an app window in Chrome, Chromium, Edge, Brave or Vivaldi
  (also Flatpak versions), otherwise in your default browser.
- Choosing folders uses `kdialog` (KDE, Steam Deck) or `zenity`. Without either,
  the launcher asks you to type the path, and you add mods by dropping them on the
  window.

## Known issues

- **Modded crash in the first Linux test:** UE4SS loaded and the Mod Loader
  mounted its mods successfully, then the game showed "An unexpected error has
  occurred" early in startup. Still to check: whether Unmodded starts, and whether
  Modded starts with all mods switched off. Reports with `ue4ss/UE4SS.log`,
  `AC8IoStoreLoader.log` and the game's own log
  (`steamapps/compatdata/2288340/pfx/drive_c/users/steamuser/AppData/Local/*/Saved/Logs`)
  help.
- Whether AC8's anti-cheat allows online play on Linux is up to the game and
  Proton, not the launcher.

## Debugging a start

Add `PROTON_LOG=1` in front of the launch line to get `~/steam-2288340.log`:

```
PROTON_LOG=1 /home/<you>/.local/share/ace8-mod-launcher/ace8-steam-launch.sh %command%
```

Remove it afterwards; it writes a large log on every start.

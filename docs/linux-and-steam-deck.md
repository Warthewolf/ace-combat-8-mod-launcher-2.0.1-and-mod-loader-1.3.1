# Linux and Steam Deck

The Linux launcher (`ACE8_Mod_Launcher_<version>_Linux_and_Mod_Loader_<loader>.zip`
on the [Releases](https://github.com/Warthewolf/ace-combat-8-mod-launcher-2.0.1-and-mod-loader-1.3.1/releases) page) does the same as the Windows one: switch between
Modded and Unmodded, manage mods, start the game. UE4SS and the Mod Loader are
the same Windows files; the game runs them through Proton.

> Status: the launcher, the Steam launch line and UE4SS loading under Proton
> have been confirmed on a Linux PC. Starting Modded still crashed the game early
> in that first test; the cause is being narrowed down (see
> [Known issues](#known-issues)).

## Set up

On the Steam Deck, do this in Desktop Mode.

1. Unzip the download, for example into your home folder. Keep the two folders
   side by side, as they come out of the zip: the launcher sets up modding from
   the `War’s Mod Loader <loader>` folder next to its own.
2. Double-click `ACE8ModLauncher` in the `ACE8 Mod Launcher <version> Linux` folder.
   If your file manager asks, choose **Execute** / **Run**. If it opens as text or
   nothing happens, right-click it → Properties → Permissions → tick
   **Is executable**, or in a terminal:
   ```sh
   chmod +x ACE8ModLauncher && ./ACE8ModLauncher
   ```
3. The launcher finds the game through Steam. If it can't, the Play page says
   **Game not found**: press **Choose the game folder**.
4. A short tour shows you around the window. Press **Next**, or
   **Skip the tour**. To see it again: **Help**, **Quick start**,
   **Show the tour again**.
5. On the Play page, press **Set up modding**. It adds UE4SS and the Mod Loader
   to the game folder. Or copy the `Game` folder from `War’s Mod Loader <loader>`
   into the game folder yourself and merge the folders.
6. Give Steam the launcher's line, once (next section). The Play page shows
   **One step in Steam**: close Steam (Steam menu → Exit) and press
   **Set it for me**, or press **Copy the line** and paste it into the game's
   launch options.
7. Add a mod: drag its `.zip` onto the launcher window, or press **Add a mod…**
   on the Mods page.
8. Pick **Modded** and press **Play modded** (or use Steam's own Play button, or
   Game Mode on the Steam Deck). For online play, pick **Unmodded**.

After the tour, the Play page shows a **First steps** list (Set up modding, Add a
mod, Play Modded) that ticks off each step when it's done.

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
Until Steam has the line, the Play page shows the same in a **One step in Steam**
card, and **Play modded** first asks with **One step in Steam first**. Steam may
only save launch options when it closes: if you've just pasted the line, choose
**It's in, play** there.

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

1. Desktop Mode: set things up as above, add your mods, and pick Modded or
   Unmodded in the launcher.
2. Game Mode: play ACE COMBAT 8 as usual. The launch line does the rest.

To switch between Modded and Unmodded later, go back to Desktop Mode.

## Where things are

| What | Where |
|---|---|
| Pak mods | `Game/Binaries/Win64/ue4ss/Mods/IoStoreLoaderMod/`, or the game's own `Game/Content/Paks/~mods/` |
| Script mods | `Game/Binaries/Win64/ue4ss/Mods/` |
| Settings, `launcher.log`, the launch script and `steam-launch.log` | `~/.local/share/ace8-mod-launcher/` |
| App menu entry | `~/.local/share/applications/ace8-mod-launcher.desktop` |
| Mod storage | `ACE COMBAT 8 - mod storage` next to the game folder (also on an SD card) |

## The window and file choosers

- The window opens as an app window in Chrome, Chromium, Edge, Brave or Vivaldi
  (also Flatpak versions), otherwise in your default browser.
- Choosing files and folders uses `kdialog` (KDE, Steam Deck) or `zenity`.
  Without either, the launcher asks you to type or paste the path instead: for
  the game folder, the mod storage folder, and **Add a mod…** /
  **Add unzipped folder…**. Dropping a mod onto the window works either way.

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

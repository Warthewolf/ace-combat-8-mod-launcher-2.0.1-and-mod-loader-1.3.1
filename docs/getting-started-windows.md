# Getting started on Windows

## What you need

- Ace Combat 8 on Steam (game version 1.1.2.0 for Mod Loader 1.3.1).
- The Windows download, `ACE8_Mod_Launcher_<version>_and_Mod_Loader_<loader>.zip`,
  from the [Releases](https://github.com/Warthewolf/ace-combat-8-mod-launcher-2.0.1-and-mod-loader-1.3.1/releases) page.
  `<version>` is the launcher's version and `<loader>` the Mod Loader's.

## Set up

1. Unzip the download anywhere, for example in Documents. It has three things.
   Keep the two folders side by side, as they come out of the zip: the launcher
   sets up modding from the Mod Loader folder next to its own.
   - `ACE8 Mod Launcher <version>` — the launcher.
   - `War’s Mod Loader <loader>` — UE4SS and the Mod Loader, laid out like the game folder.
   - `READ ME FIRST.txt`.
2. Open `ACE8 Mod Launcher <version>` and double-click `ACE8ModLauncher.exe`.
   The first time, Windows may say "Windows protected your PC": press
   **More info**, then **Run anyway**. The launcher is new and not signed, so
   Windows doesn't know it yet.
3. The launcher finds the game through Steam. If it can't, the Play page says
   **Game not found**: press **Choose the game folder** and pick the
   `ACE COMBAT 8` folder. To find it in Steam: right-click **ACE COMBAT 8**,
   **Manage**, **Browse local files**.
4. A short tour shows you around the window, one thing at a time. Press
   **Next**, or **Skip the tour**. To see it again: **Help**, **Quick start**,
   **Show the tour again**, or **Show the tour** at the bottom of the menu.
5. On the Play page, press **Set up modding** (in the tour, **Set up now** does
   the same). It adds UE4SS and the Mod Loader to the game folder. The game's own
   files aren't changed. You only do this once; the launcher doesn't need the Mod
   Loader folder after that. If the game has them already, skip this step.
   - **By hand instead:** copy the `Game` folder from `War’s Mod Loader <loader>`
     into `...\steamapps\common\ACE COMBAT 8\` and let Windows merge the folders.
     The launcher notices by itself.
6. Add a mod: drag its `.zip` anywhere onto the launcher window, or press
   **Add a mod…** on the Mods page. See [Managing mods](managing-mods.md).
7. Pick **Modded** and press **Play modded**. For online play, pick **Unmodded**
   and press **Play unmodded**.

After the tour, the Play page shows a **First steps** list: Set up modding, Add a
mod, Play Modded. Each step has its own button and ticks itself off when it's
done. **Hide** hides the list; Help, Quick start has the same steps.

When it's set up, `Game\Binaries\Win64` in the game folder looks like this:

```
Win64\
  AceCombat8.exe
  dwmapi.dll          <- starts UE4SS
  ue4ss\              <- UE4SS, the Mod Loader (ue4ss\Mods\IoStoreLoaderMod) and script mods
  ...the game's own files
```

## The window

- **Play:** the switch between **Modded** and **Unmodded**, the Play button, and
  after a Modded start, **Last game start**.
- **Mods:** **Your mods** (the pak mods, in load order), **Script mods** and
  **Removed mods**, with **Add a mod…** at the top.
- **Settings:** the game folder, what is installed (**Modding setup**), the mod
  storage folder, **Support**, and how the game is started.
- **Help:** a Quick start, the words the launcher uses, and the usual fixes.

The bar along the top shows the game, the mode, the Mod Loader, your mods and the
last game start. Press one to go to its page. **Auto**, **Night** and **Day** at
the bottom of the menu change the colours.

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

- Always use the launcher's **Play modded** for Modded. Steam's own Play button
  turns anti-cheat on, which stops UE4SS and the mods.
- Steam has to be running and signed in; the launcher opens it if needed and
  waits for it to sign in.
- If the Play page says **One Microsoft install needed**, UE4SS needs
  Microsoft's free Visual C++ runtime: press **Download from Microsoft**, open
  `VC_redist.x64.exe`, press **Install**, then press Play.
- You don't need Steam launch options, `AC8_Offline.json` or `ac8noeac.bat` any
  more. If Steam's launch options still have the old anti-cheat line, the
  launcher tells you, so Unmodded can start with anti-cheat on.

After a Modded start, **Last game start** on the Play page lists each mod as
Loaded, Not loaded or Failed, with the reason.

## Settings worth knowing

- **Game folder:** **Change…** if the launcher found the wrong copy of the game.
- **Modding setup:** what is installed, with **Update Mod Loader** when the Mod
  Loader folder next to the launcher has a newer one, or **Repair**, which puts
  back missing UE4SS or Mod Loader files.
- **Mod storage folder:** **Change…** chooses where switched-off and removed mods
  are kept, and moves them there. On the same drive as the game, switching is
  instant; on another drive, each switch copies the files.
- **Advanced: how the game is started:** **Extra launch options for Modded**, for
  example `-dx11`.
- **Support:** **Copy support report** copies your setup, mods and the latest
  logs, ready to paste into a message. Your Windows user name is left out. The
  **Mod Loader log**, **UE4SS log** and **Launcher log** buttons show the logs.

## Updating the launcher

Close it, replace `ACE8ModLauncher.exe` with the new one, start it. Settings,
mods and load order stay. Since 2.0.1, starting a new version while an older one
is still open closes the older one.

If the new download has a newer Mod Loader, keep its `War’s Mod Loader` folder
next to the launcher's folder. While Modded, the Play page then shows
**Mod Loader update** with an **Update** button (also **Update Mod Loader** in
Settings, Modding setup). Your mods and load order stay.

## Antivirus

The launcher is new and not code-signed, so some antivirus programs (Norton,
Microsoft Defender) may flag it the first time; Defender's
`Trojan:Win32/Wacatac.B!ml` is a machine-learning guess, not a known threat.
Restore it from quarantine and add an exclusion. See
[Troubleshooting](troubleshooting.md#antivirus-warnings).

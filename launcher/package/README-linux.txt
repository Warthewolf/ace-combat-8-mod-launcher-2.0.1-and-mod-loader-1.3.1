ACE8 Mod Launcher 2.0.1 for Linux and Steam Deck
================================================

One window for modding Ace Combat 8: switch between Modded and Unmodded,
manage your mods and their load order, and start the game.

This is the Linux version. On Windows, use the Windows download.

Any issues please contact @voidcallsreaper on Discord.


GETTING STARTED
---------------
1. Unzip the download anywhere you like, for example your home folder.
   On the Steam Deck, do this in Desktop Mode.
2. Double-click ACE8ModLauncher. If your file manager asks, choose
   "Execute" or "Run". If it opens as a text file or nothing happens,
   right-click it, Properties, Permissions, and tick "Is executable"
   (or run: chmod +x ACE8ModLauncher).
3. The launcher finds the game through Steam. If it can't, it asks you to
   choose the ACE COMBAT 8 folder.
4. If UE4SS and the Mod Loader aren't in the game yet, press "Install"
   (it uses the "War’s Mod Loader 1.3.1" folder next to the launcher's
   folder), or copy that folder's Game folder into the ACE COMBAT 8 folder
   yourself and merge the folders. The launcher doesn't need it after that.
5. One time only: add the launcher's line to the game's Steam launch
   options (see below). The launcher shows you the line and can set it for
   you.
6. Choose Modded or Unmodded, then press Play.

After the first start, the launcher is also in your app menu (Games), so you
can start it from there or add it to Steam as a non-Steam game.


THE STEAM LAUNCH OPTIONS LINE
-----------------------------
On Linux the game runs through Proton, so Steam has to start it. For Modded,
Steam needs this in the game's launch options:

  "<your home>/.local/share/ace8-mod-launcher/ace8-steam-launch.sh" %command%

The launcher shows the exact line for your computer in Settings > Starting
the game, with a Copy button. To add it:
  right-click ACE COMBAT 8 > Properties > General > Launch Options, paste it.
Or press "Set it for me": with Steam closed (Steam menu > Exit), the launcher
adds it to Steam's settings itself and keeps a copy of the old settings.

The line runs a small script each time Steam starts the game:
  Modded     it starts AceCombat8.exe with anti-cheat off
             (-eac-nop-loaded -nosplash, like ac8noeac.bat on Windows) and
             tells Proton to load UE4SS (WINEDLLOVERRIDES="dwmapi=n,b").
  Unmodded   it changes nothing: the game starts with anti-cheat on.
So the line can stay in Steam for good. Steam's own Play button, and Game
Mode on the Steam Deck, then always start the game the right way for the
mode you chose in the launcher.

Other launch options can stay: put the launcher's line just before
%command%, for example
  gamemoderun "<...>/ace8-steam-launch.sh" %command% -dx11
"Set it for me" and the Copy button keep the options you already have.

With the Flatpak version of Steam, the script goes in
~/.var/app/com.valvesoftware.Steam/ace8-mod-launcher, where Steam can see it.
The launcher works this out for you.


MODDED AND UNMODDED
-------------------
Modded     UE4SS, the Mod Loader and your mods are in the game. Steam starts
           the game without anti-cheat. Offline and single player only.

Unmodded   UE4SS, the Mod Loader and all your mods are moved out of the game
           into the mod storage folder. Steam starts the game with
           anti-cheat on.

Switching moves the files within the same drive, so it normally takes a
moment. Nothing is deleted. If a switch can't finish, everything is put back
the way it was.

What gets moved:
  Game/Binaries/Win64/dwmapi.dll
  Game/Binaries/Win64/ue4ss          (UE4SS, the Mod Loader and all its mods)
  Game/Content/Paks/~mods
  Game/Content/Paks/LogicMods

Where it goes while Unmodded: the mod storage folder, under "Switched off".
By default that is .../steamapps/common/ACE COMBAT 8 - mod storage, next to
the game (on the SD card too, if the game is there). You can choose another
place in Settings > Mod storage folder; the launcher moves everything in it
for you. On the same drive as the game, switching is instant; on another
drive, each switch copies the files.

Steam Deck: switch between Modded and Unmodded in Desktop Mode, then play
from Game Mode as usual.


ADDING AND REMOVING MODS
------------------------
- Drop a mod's .zip or folder onto the launcher window, or press
  "Add mods..." on the Mods page. The launcher puts each part where it
  belongs.
- Pak mods are .utoc and .ucas files, or a file ending in _P.pak (text,
  fonts and other files the game keeps in .pak files). _P.pak mods need
  Mod Loader 1.3.1 or newer; Settings > Modding setup updates it.
- If a download holds several versions of a mod, it asks which one you want.
- Mods you add while Unmodded are kept with the switched-off files, and come
  into the game when you switch to Modded.
- .7z and .rar files: unpack them first, then add the folder.
- Remove moves a mod to "Removed mods" in the mod storage folder. Restore
  it, or delete it for good, from the Mods page.


LOAD ORDER
----------
Mods lower in the list load later, so they win when two mods change the same
game file. The Mods page tells you when two mods overlap and which one wins.
Drag a mod by its handle, or use the arrow buttons. The order is saved in the
Mod Loader's loadorder.txt, so it is exactly the order the Mod Loader uses.


WHERE THINGS ARE
----------------
Pak mods           Game/Binaries/Win64/ue4ss/Mods/IoStoreLoaderMod
Script mods        Game/Binaries/Win64/ue4ss/Mods
Mod storage        "ACE COMBAT 8 - mod storage" next to the game folder,
                   or the folder you chose in Settings
Launcher settings  ~/.local/share/ace8-mod-launcher
                   (settings.json, launcher.log, the Steam launch script and
                   steam-launch.log, which notes each start from Steam)
App menu entry     ~/.local/share/applications/ace8-mod-launcher.desktop


IF SOMETHING GOES WRONG
-----------------------
- After starting the game Modded, the Play page shows "Last game start":
  which mods loaded and which didn't, and why.
- If the game starts Modded but the mods don't load, check Settings >
  Starting the game. "Last start from Steam" says how Steam last started the
  game. If it is old or missing, Steam didn't use the launcher's line.
- Steam may only save launch options when it closes. If you've just pasted
  the line and the launcher still says it is missing, press Play and choose
  "It's in, play".
- Settings > "Copy support report" copies your setup, your mods and the
  last logs, ready to paste into a message. Your user name is left out.
- The window opens as an app window in Chrome, Chromium, Edge, Brave or
  Vivaldi when one is installed (also as a Flatpak), otherwise in your web
  browser.
- Choosing folders uses zenity or kdialog (KDE and the Steam Deck have
  kdialog). Without either, the launcher asks you to type the folder's path,
  and you add mods by dropping them onto the window.
- Whether the game itself runs well under Proton, and whether its
  anti-cheat accepts online play on Linux, is up to the game and Proton,
  not the launcher.

Any issues please contact @voidcallsreaper on Discord.


WHAT'S IN THIS FOLDER
---------------------
ACE8ModLauncher       the launcher (64-bit Linux, x86-64; runs on the
                      Steam Deck)
README.txt            this file
CHANGELOG.txt         what changed
Big Shoulders font licence.txt   the licence of the font the window uses

The Mod Loader download next to this folder holds UE4SS and the Mod Loader:
the same Windows files as for Windows, which the game runs through Proton.
UE4SS is MIT licensed: see ue4ss/LICENSE in the Mod Loader download.
The window uses the Big Shoulders font, under the SIL Open Font License: see
Big Shoulders font licence.txt.

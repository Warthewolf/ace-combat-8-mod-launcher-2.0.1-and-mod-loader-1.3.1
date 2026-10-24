ACE8 Mod Launcher 2.0.1
=======================

One window for modding Ace Combat 8: switch between Modded and Unmodded,
manage your mods and their load order, and start the game.

Any issues please contact @voidcallsreaper on Discord.

This is the Windows version. Linux and Steam Deck have their own download.


GETTING STARTED
---------------
1. Unzip the download anywhere you like, for example Documents.
2. Double-click ACE8ModLauncher.exe.
   The first time, Windows may say "Windows protected your PC" because the
   launcher is new. Press "More info", then "Run anyway".
3. The launcher finds the game through Steam. If it can't, it asks you to
   choose the ACE COMBAT 8 folder.
4. If UE4SS and the Mod Loader aren't in the game yet: copy the Game folder
   from the "War’s Mod Loader 1.3.1" folder into the ACE COMBAT 8 folder and
   let Windows merge the folders. (Or press Install in the launcher: it uses
   that folder when it is next to the launcher's folder.) The launcher
   doesn't need it after that.
5. Choose Modded or Unmodded, then press Play.


MODDED AND UNMODDED
-------------------
Modded     UE4SS, the Mod Loader and your mods are in the game. Play starts
           the game directly, without anti-cheat (the same way as
           ac8noeac.bat). Offline and single player only.

Unmodded   UE4SS, the Mod Loader and all your mods are moved out of the game
           into the mod storage folder. Play starts the game through Steam
           with anti-cheat on, so online play works.

Switching moves the files within the same drive, so it normally takes a
moment. Nothing is deleted. If a switch can't finish (for example because a
file is open in another program), everything is put back the way it was.

What gets moved:
  Game\Binaries\Win64\dwmapi.dll
  Game\Binaries\Win64\ue4ss          (UE4SS, the Mod Loader and all its mods)
  Game\Content\Paks\~mods
  Game\Content\Paks\LogicMods

Where it goes while Unmodded: the mod storage folder, under "Switched off".
By default that is ...\steamapps\common\ACE COMBAT 8 - mod storage, next to
the game. You can choose another place in Settings > Mod storage folder; the
launcher moves everything in it for you. On the same drive as the game,
switching is instant; on another drive, each switch copies the files.


YOU NO LONGER NEED STEAM LAUNCH OPTIONS
---------------------------------------
If you used the old way (the "cmd /c set EOS_USE_ANTICHEATCLIENTNULL..."
line), clear it in Steam:
  right-click ACE COMBAT 8 > Properties > General > Launch options
With that box empty, Steam's Play button and Unmodded start the game
normally, with anti-cheat on. The launcher tells you if the line is still set.

AC8_Offline.json and ac8noeac.bat are not used by the launcher. You can leave
them where they are.

Steam has to be running and signed in to play Modded; the launcher opens it
if it isn't, and waits for it to sign in.

Always start a Modded game with the launcher's Play button. Steam's own Play
button turns anti-cheat on, and that doesn't work with UE4SS and the mods.


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
- Skins made with ACE8 Skin Studio's skin slots are added with Skin Studio
  (Skin slots > Import), so they get a slot of their own.
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
Pak mods           Game\Binaries\Win64\ue4ss\Mods\IoStoreLoaderMod
Script mods        Game\Binaries\Win64\ue4ss\Mods
Mod storage        "ACE COMBAT 8 - mod storage" next to the game folder,
                   or the folder you chose in Settings
Launcher settings  %LOCALAPPDATA%\ACE8 Mod Launcher
                   (settings.json and launcher.log)


IF SOMETHING GOES WRONG
-----------------------
- After starting the game Modded, the Play page shows "Last game start":
  which mods loaded and which didn't, and why.
- Settings > "Copy a support report" copies your setup, your mods and the
  last logs, ready to paste into a message. Your Windows user name is left
  out.
- If a switch says a program has a file or folder open: close the game,
  Explorer windows showing the game's folders, editors (VS Code,
  Notepad++) and command windows, then switch again. The launcher moves
  what it can around a folder that is held open, so this is rare.
- If AceCombat8.exe is set to run as administrator, Windows asks for
  permission each time you play Modded. To stop that, right-click
  AceCombat8.exe, choose Properties, then Compatibility, and untick "Run this
  program as an administrator".
- Antivirus warnings: the launcher is new and not signed, so some antivirus
  programs are wary of it the first time. It starts no scripts or hidden
  programs. If yours blocks it, restore it from the antivirus's quarantine
  and allow it.
- The launcher's window is a Microsoft Edge app window. If Edge is missing,
  the launcher opens in your web browser instead.
- UE4SS needs the Microsoft Visual C++ 2015-2022 runtime (x64). The launcher
  tells you if it is missing and links to Microsoft's download.

Any issues please contact @voidcallsreaper on Discord.


WHAT'S IN THIS FOLDER
---------------------
ACE8ModLauncher.exe   the launcher
README.txt            this file
CHANGELOG.txt         what changed
Big Shoulders font licence.txt   the licence of the font the window uses

UE4SS is MIT licensed: see ue4ss\LICENSE in the Mod Loader download.
The window uses the Big Shoulders font, under the SIL Open Font License: see
Big Shoulders font licence.txt.

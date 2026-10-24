AC8 Mod Loader 1.3.1
====================

Loads .utoc/.ucas mods and *_P.pak mods (text, fonts and other files the game
keeps in .pak files) into Ace Combat 8 (PC, version 1.1.2.0).
It runs as a UE4SS mod. There is one version for every mod: you never edit
the loader, and you never have to keep a list of mods up to date.


Install
-------
You need UE4SS installed for the game first (dwmapi.dll next to
AceCombat8.exe, and a "ue4ss" folder).

1. Close the game.
2. Copy the "ue4ss" folder from this download into
       ...\steamapps\common\ACE COMBAT 8\Game\Binaries\Win64\
   and let Windows merge the folders. If you already have an
   IoStoreLoaderMod folder, let Windows replace its files. Your mods and your
   loadorder.txt are not touched.
3. That's it. The empty file enabled.txt in IoStoreLoaderMod tells UE4SS to
   start the loader, so mods.txt does not need a line for it.
   (If your mods.txt has the line "IoStoreLoaderMod : 0", change the 0 to 1
   or delete the line.)

Keep only ONE copy of the loader. If another folder under ue4ss\Mods also has
a dlls\main.dll that is an older mod loader, delete that folder. The log names
the other copy if there is one.


Adding mods
-----------
A mod can be a standalone *_P.pak, a .utoc/.ucas pair, or a set of
*_P.pak / *_P.utoc / *_P.ucas files with the same base name. Keep all files
supplied by the mod together. Only Pak names ending in _P.pak are actively
loaded (case insensitive); standalone UTOC files keep the existing naming rule.
Put them in either place:

  A) A folder of their own inside IoStoreLoaderMod:
         ue4ss\Mods\IoStoreLoaderMod\Some Mod\SomeMod_P.utoc  (+ .ucas)
     or: ue4ss\Mods\IoStoreLoaderMod\Some Mod\SomeMod_P.pak   (+ sidecars)

  B) The game's usual mods folder:
         ACE COMBAT 8\Game\Content\Paks\~mods\SomeMod_P.utoc  (+ .ucas)
     (create the ~mods folder if it is not there)

Extra folders from unzipping are fine: "Some Mod\Some Mod v1.2\SomeMod_P.utoc"
loads too. A .utoc or *_P.pak put straight into IoStoreLoaderMod (not in a
folder) is also discovered.

When a *_P.pak has a same-directory *_P.utoc, the game's Legacy Pak mount
handles that sidecar; the loader does not mount it again. Its .ucas must also
be present. A Pak without a UTOC does not need a .ucas.

Active Pak mounting needs the game's real FPakPlatformFile object. If the
loader has not captured it yet, Pak requests are deferred until a later native
mount provides it. If no such mount occurs, those Paks remain unloaded.

Folders with these names are never loaded, so keep old versions in them:
    backup, backups, old, disabled
(build, packed, export, source, dlls and Scripts are skipped as well; they
hold tools' working files.)

If a download has several versions of a mod in separate folders (for example
"Blue" and "Red"), copy only the one you want. Everything in a mod's folder
loads.


Which mod wins
--------------
Mods in IoStoreLoaderMod load first, A to Z, then the ones in ~mods, A to Z.
A mod that loads later wins when two mods change the same game file.

If the same file name (for example SomeMod_P.utoc) is in two places, only the
newest copy loads. A .utoc without its .ucas is skipped. Both are written to
the log, and ACE8 Mod Launcher shows them too.

Pak and UTOC entries with the same base filename share this duplicate rule.
Paks already present in the game's native mount table are skipped as well.


Changing the order or switching mods off (optional)
---------------------------------------------------
Use ACE8 Mod Launcher. Its Mods page switches mods on and off and changes
the order (drag a mod, or use the arrow buttons), and shows how the last game
start went. Changes apply the next time the game starts.

loadorder.txt can also be edited by hand: one mod per line, "+Name" is on,
"-Name" is off. Mods in ~mods are written "~mods\Name". Mods that are not in
the file still load, after the listed ones. A name that no longer exists is
ignored, so the file never needs cleaning up.

For loose Pak files, use the full filename, e.g. +SomeMod_P.pak. This switch
also covers its paired UTOC. For a subfolder, use the folder name as before.
These switches control the loader's plan; they cannot stop the game's own
scan of Paks\~mods. For loader-controlled disabling, use IoStoreLoaderMod.
ACE8 Mod Launcher lists .pak mods too, and keeps these lines when it saves.


Checking it worked
------------------
Open AC8IoStoreLoader.log in IoStoreLoaderMod (or "Show the Mod Loader's log" in
ACE8 Mod Launcher). A good start ends with lines like

    done: 3 of 3 attempted mount requests returned success (3 planned, 3 mod(s))
    new files: 3 explicit UTOC containers handed to the game's package store (3 UTOC successes this pass); Legacy sidecars are registered by the engine

The package-store summary counts the loader's direct UTOC registrations.
Paired UTOC registration during a Pak mount belongs to the engine instead.

For active Pak loading, look for:

    Legacy Pak: captured native FPakPlatformFile=...
    mounting Legacy Pak [...] order=... path=...
    Legacy result AL=... pak-in-table=...

AL=0 with pak-in-table=1 means the Pak entered the native table, but the full
request returned failure (possibly a missing or failed sidecar). It does not
prove the whole mod works. Check the sidecar result and the game itself.

What the other lines mean:

  "patterns not found exactly once"
      The game was updated, or this is a different version. The loader
      changes nothing. It needs an update for the new game version.

  "the game's mount function is already hooked by: <file>"
      Another copy of a mod loader is installed. Delete that one.

  "Legacy Pak active mounting unavailable"
      The Legacy Mount entry or its native IoStore call could not be verified.
      Active Pak mounting is disabled; the existing UTOC path is retained.

  "Legacy Pak: deferred until a verified native platformFile is captured"
      UTOC loading can continue. Pak loading waits for another native mount;
      it cannot proceed if the game's platform-file object is never captured.

  "skipped ...: its .ucas file is missing"
      Copy the .ucas next to the .utoc.

  "skipped ...: same name as ..."
      The same mod is installed twice. Delete the copy you don't want.

  "result code=<not 0>"
      The game refused that file; the message on the same line says why.
      The other mods still load.

  "package store: ..."
      The loader tells the game which other files each mod file needs.
      Mods that make an aircraft use different weapons or parts depend on
      this. "not recognised" means this step was skipped for that file.

  "new files: ..."
      Mods can add files the game does not have (for example new skins
      with their own textures and icons): the loader hands every mod to
      the game's package store, the way the game does with its own files.
      If a line says "mods can replace game files but not add new ones",
      that step was not possible this time; mods that only replace game
      files still work.

  "the loader started after the game mounted pakchunk0..."
      UE4SS started late this time; the mods were loaded a moment later
      instead. Nothing to do.

  The log stops at "hook installed, waiting..."
      The game loaded its files before UE4SS started the loader.
      Start the game again.

  No log at all
      UE4SS did not start the loader. Check that enabled.txt and
      dlls\main.dll are in IoStoreLoaderMod, and that UE4SS itself runs
      (ue4ss\UE4SS.log is written at each start).


For mod makers
--------------
  - Ship all required files together in a folder. A Pak must end in _P.pak;
    keep its matching .utoc/.ucas when supplied. Tell people to put the folder
    in IoStoreLoaderMod or in Paks\~mods.
  - Give the files a name nobody else will use, e.g. YourName_ModName_P.
    Two files with the same name cannot both load.
  - Do not ship a loadorder.txt: it would replace the player's own.
  - Mods can replace files the game already has, and (since 1.1.0) add
    brand-new files at new asset paths. A mod that adds new files needs
    1.1.0 or later; with an older loader its new files do not load.


Limits
------
  - Game version 1.1.2.0 only. After a game update the loader does nothing
    (and says so in the log) until it is updated.
  - Single player / offline use only.
  - Active Pak mounting supports *_P.pak only and depends on the verified
    native mount entry and captured platform-file object.
  - This remains a UE4SS start_mod loader, not an early proxy DLL. It cannot
    undo a failed native mount or repair a partially mounted Pak.


Uninstall
---------
Move your mod folders out of IoStoreLoaderMod, then delete the
IoStoreLoaderMod folder.


Source
------
"source" contains the build and test sources: source\src\build-MSVC.ps1 or
build-LLVM.sh to build, source\test for the tests. Builds go to source\build,
not to dlls\main.dll. The game never uses the source folder. Never install
the UE4SS.dll test stub the tests build.

Based on the single-mod IoStoreLoaderMod 0.1.0 DLL for AC8 1.1.2.0; the hook
and byte patterns are the same.

Any issues please contact @voidcallsreaper on Discord.

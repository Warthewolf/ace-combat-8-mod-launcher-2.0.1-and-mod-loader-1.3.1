# Managing mods

Everything here is on the launcher's **Mods** page. Changes apply the next time
the game starts.

## Adding a mod

Drop the mod's `.zip` or folder anywhere on the launcher window, or press
**Add a mod…** (a `.zip`, or a mod's `.utoc`, `.ucas` and `.pak` files) or
**Add unzipped folder…** (a mod you have already unzipped). The launcher looks
inside and puts each part where it belongs:

| The download has | It goes to |
|---|---|
| `.utoc` + `.ucas` (+ `.pak`) files | `ue4ss\Mods\IoStoreLoaderMod\<Mod name>\` |
| A file ending in `_P.pak` (text, fonts, other `.pak` content) | `ue4ss\Mods\IoStoreLoaderMod\<Mod name>\` — needs Mod Loader 1.3.1 |
| A folder with `Scripts\main.lua` or `dlls\main.dll` (UE4SS script mod) | `ue4ss\Mods\<Mod name>\` |
| A `~mods` folder | `Game\Content\Paks\~mods\` |
| A `LogicMods` folder | `Game\Content\Paks\LogicMods\` (needs UE4SS's BPModLoaderMod) |

Then the launcher shows the Mods page with what it added or updated marked
**New**, until you leave the page.

Good to know:

- If modding isn't set up yet, the launcher offers to do that first
  (**Set up and add**), then adds the mod.
- If the download holds several versions of a mod (for example "Blue" and "Red"
  folders), the launcher asks which one you want (**Which version?**).
- Adding a mod you already have updates it: see [Updating a mod](#updating-a-mod).
- Mods added while Unmodded go with the switched-off files and come into the game
  when you switch to Modded.
- `.7z` and `.rar` files: unpack them first, then add the folder.
- Skins made with ACE8 Skin Studio's skin slots are added in Skin Studio
  (Skin slots → Import), so they get a slot of their own.
- A plain `.pak` whose name doesn't end in `_P.pak` is not loaded by the Mod
  Loader; the launcher says so.

## Updating a mod

Add the newer version the same way. It overwrites the installed version in
place; no copy of the old version is kept. Each mod on the Mods page shows the
date it was last updated ("Updated today", "Updated 6 Nov"), or added.

- **Pak mods** are replaced as a whole, so no old file can load next to the new one.
- **Script mods**: the new version's files overwrite the old ones, and everything
  else you or other mods added is kept (settings saved while playing, add-ons
  other mods put in its folder). A mod can also name files where your copy wins
  (see [For mod makers](mod-makers.md#ace8-keeptxt-files-the-players-copy-keeps)).
- **UE4SS's shared folder** (`ue4ss\Mods\shared`) is updated file by file.
- A script mod you had switched off stays off.
- If an update can't finish (a file is held open, the disk is full), the old
  version is put back as it was.

## Load order

Pak mods are listed under **Your mods** in load order: **lower in the list loads
later and wins** when two mods change the same game file. To change the order,
drag a mod by its handle (the six dots on the left), use its ▲ ▼ buttons, or
select it and press Alt + ↑ / ↓.

The Mods page reads each mod's file list and tells you when two mods overlap
("1 game file is also changed by X. That mod loads later, so its version is the
one you see.").

The order is saved in the Mod Loader's own `loadorder.txt`
(`ue4ss\Mods\IoStoreLoaderMod\loadorder.txt`), so it is exactly what the Mod
Loader uses:

```
# AC8 mod load order - written by ACE8 Mod Launcher. This file is optional.
+Void_Wolf
+Ladon_PlayableAircraft
-Guided TLS
+~mods\SomeMod_P
+zz_SkinStudio_SkinList
```

`+Name` is on, `-Name` is off. Mods not in the file load after the listed ones,
A to Z. Skin Studio's skin list (`zz_SkinStudio_SkinList`) is always kept last,
because it has to load after the skins.

## Switching mods on and off

Each mod has a switch. Off means it stays installed but doesn't load.

- **Pak mods:** the switch writes `+` or `-` in `loadorder.txt`. The files stay
  where they are.
- **Script mods:** the switch renames the mod's `enabled.txt` to `enabled.off`
  (and back) and updates `ue4ss\Mods\mods.txt`.
- Mods tagged **~mods** are in `Paks\~mods`, which the game also reads by itself,
  so switching one off may not stop it: it only stops the Mod Loader's part. To
  switch such a mod off for good, remove it.

Each row also has **Files**, which shows the mod's files in Explorer (a folder
window on Linux), and pak mods have **Changes N game files**, the list of game
files the mod changes.

## Removing, restoring, deleting

**Remove** moves a mod to **Removed mods**, at the bottom of the Mods page; its
files wait in the mod storage folder. Right after removing, the message has
**Undo**. Later, **Restore** puts it back where it was, and **Delete** removes it
for good (it asks first). Each removed mod has a small `removed.json` describing
where it came from.

Adding and removing need the game closed. Switching mods on or off and changing
the order work while it runs, and count at the next start.

## Last game start

After a Modded start, the Play page shows **Last game start**:

- each pak mod switched on, marked **Loaded**, **Not loaded** (with the reason)
  or **Failed**, read from the Mod Loader's log (`AC8IoStoreLoader.log`);
- each script mod switched on, marked **Started** or **Not started**, read from
  UE4SS's log (`ue4ss\UE4SS.log`), plus any Lua errors UE4SS reported.

**Mod Loader log** and **UE4SS log** next to the heading (also in Settings,
Support) show the whole logs. The Mod Loader only handles pak mods; script mods
are started by UE4SS. On the Mods page, each pak mod that was switched on at that
start is also tagged **Loaded** or **Failed**, and mods that need a look are
counted next to **Mods** in the menu.

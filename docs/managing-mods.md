# Managing mods

Everything here is on the launcher's **Mods** page. Changes apply the next time
the game starts.

## Adding a mod

Drop the mod's `.zip` or folder anywhere on the launcher window, or press
**Add mods…** / **Add folder…**. The launcher looks inside and puts each part
where it belongs:

| The download has | It goes to |
|---|---|
| `.utoc` + `.ucas` (+ `.pak`) files | `ue4ss\Mods\IoStoreLoaderMod\<Mod name>\` |
| A file ending in `_P.pak` (text, fonts, other `.pak` content) | `ue4ss\Mods\IoStoreLoaderMod\<Mod name>\` — needs Mod Loader 1.3.1 |
| A folder with `Scripts\main.lua` or `dlls\main.dll` (UE4SS script mod) | `ue4ss\Mods\<Mod name>\` |
| A `~mods` folder | `Game\Content\Paks\~mods\` |
| A `LogicMods` folder | `Game\Content\Paks\LogicMods\` (needs UE4SS's BPModLoaderMod) |

Good to know:

- If the download holds several versions of a mod (for example "Blue" and "Red"
  folders), the launcher asks which one you want.
- An older copy of the same mod is moved to **Removed mods**, not deleted.
- Mods added while Unmodded go with the switched-off files and come into the game
  when you switch to Modded.
- `.7z` and `.rar` files: unpack them first, then add the folder.
- Skins made with ACE8 Skin Studio's skin slots are added in Skin Studio
  (Skin slots → Import), so they get a slot of their own.
- A plain `.pak` whose name doesn't end in `_P.pak` is not loaded by the Mod
  Loader; the launcher says so.

## Load order

Pak mods are listed in load order: **lower in the list loads later and wins**
when two mods change the same game file. Drag a mod by its handle, or select it
and press Alt + ↑ / ↓.

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

- **Pak mods:** the switch next to each mod writes `+` or `-` in `loadorder.txt`.
  The files stay where they are.
- **Script mods:** the switch renames the mod's `enabled.txt` to `enabled.off`
  (and back) and updates `ue4ss\Mods\mods.txt`.
- Mods in `Paks\~mods` are also loaded by the game itself, so switching one off
  only stops the Mod Loader's part. To switch such a mod off for good, remove it.

## Removing, restoring, deleting

**Remove** moves a mod to **Removed mods** in the mod storage folder. From there,
**Restore** puts it back where it was, and **Delete** removes it for good. Each
removed mod has a small `removed.json` describing where it came from.

## Last game start

After a Modded start, the Play page shows **Last game start**:

- each pak mod switched on, marked **Loaded**, **Not loaded** (with the reason)
  or **Failed**, read from the Mod Loader's log (`AC8IoStoreLoader.log`);
- each script mod switched on, marked **Started** or **Not started**, read from
  UE4SS's log (`ue4ss\UE4SS.log`), plus any Lua errors UE4SS reported.

**Mod Loader log** and **UE4SS log** show the whole logs. The Mod Loader only
handles pak mods; script mods are started by UE4SS.

# After a game update

Mod Loader 1.3.1 is built for Ace Combat 8 version **1.1.2.0**. A game update can
stop it working until it is updated.

## What can break

| Part | Why | How you notice |
|---|---|---|
| Mod Loader | It finds its hook points in `AceCombat8.exe` by byte patterns. If the update changes that code, they no longer match | Last game start: "Game version not supported"; the log says `patterns not found exactly once`. The game still starts, without pak mods |
| UE4SS | It also searches the exe, with broader patterns | `ue4ss\UE4SS.log` names what it couldn't find |
| Pak mods | A mod that replaces a game file the update changed can crash the game or show stock content | The game crashes, or a mod looks stock |
| Anti-cheat-off start | `-eac-nop-loaded` may change | Modded doesn't start |

The launcher itself doesn't depend on the game version.

## Before the update

1. **Switch to Unmodded.** Steam then patches a clean game, and your mods wait in
   the mod storage folder, which Steam never touches.
2. **Keep a copy of the current `AceCombat8.exe`** (about 540 MB) outside the game
   folder, for example `Documents\AC8 1.1.2.0\AceCombat8.exe`. With the old and new
   exe side by side, finding the Mod Loader's patterns again is mechanical; without
   it, much slower.
3. Optional, to keep playing modded until the Mod Loader is updated: Steam →
   ACE COMBAT 8 → Properties → Updates → **Only update this game when I launch it**.
   The launcher's Modded Play starts the exe directly, not through Steam, so it
   keeps running the current version offline. (Windows only; on Linux, Steam
   always starts the game.)

## After the update

1. Start **Unmodded**: check the game runs and online play works.
2. Switch to **Modded** and start the game. Then check **Last game start**:
   - **Mods loaded:** done.
   - **"Game version not supported":** the Mod Loader needs new patterns. See
     [Updating the patterns](mod-loader-internals.md#updating-the-patterns-after-a-game-update).
   - **Mods loaded, but the game crashes:** switch mods off in halves on the Mods
     page until it starts; update or remove the mod that breaks it.

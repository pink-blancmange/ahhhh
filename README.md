# APPAPPAPP (`ahhhh`)

A DIY Linux AppImage workbench and manager written in Go with an embedded punk/lo-fi web UI.

Browse the public AppImage catalog, drop in local `.AppImage` files from anywhere on the web, extract icons, wire up desktop menu launchers automatically, check for GitHub release updates, and crate-dig through apps using a 6-sided die or a pile of 3.5" floppies and cassette tapes.

> **Platform Note:** AppImages are Linux-only. `ahhhh` targets Linux (`amd64` and `arm64`).

---

<img width="1343" height="935" alt="ahhhh" src="https://github.com/user-attachments/assets/4de43170-187d-4f36-829f-c6b07561e55f" />

## Features

- **Single-Binary Web UI & CLI:** Compiles to one self-contained Go binary with the web frontend embedded—no Node, Electron, or runtime dependencies.
- **One-Click Catalog Installs:** Fetches the latest `.AppImage` release from GitHub, installs to `/opt/appimages/`, symlinks into `/usr/local/bin/`, extracts the app icon, and generates a `.desktop` entry so it appears in your desktop app menu.
- **DIY // Local Drop Box:** Drag-and-drop (or pick) any `.AppImage` file downloaded manually, optionally give it a custom name, and install it with the exact same desktop integration.
- **Updates & Clean Removal:** Compare installed GitHub apps against latest releases and update in place, or cleanly remove the binary, symlink, icon, and menu entry.
- **Crate-Digging Toy Crate:**
  - **Dice Roll:** Roll 1–6 to deal out random floppies and cassettes from your active category or filter.
  - **Rummage:** Tip a scattered pile of disks and tapes across the desk and click any piece to inspect or install it.
- **Responsive Viewport Grid:** Crate-divider category tabs, live search with synonyms, random or A–Z sorting, keyboard navigation (`ArrowLeft` / `ArrowRight` / `Escape`), and an inline **Clicky clicky FAQ FAQ**.

---

## Quick Install (Linux)

### One-Line Installer (Binary + GUI Menu Launcher)

Downloads the matching `amd64` or `arm64` release binary to `/usr/local/bin/ahhhh` and creates an **Ahhhh** launcher in your desktop app menu:

```sh
curl -fsSL https://raw.githubusercontent.com/pink-blancmange/ahhhh/main/install.sh | sh

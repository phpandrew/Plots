# Plots — desktop groups for Windows

A lightweight Stardock Fences–style organizer. Single `Plots.exe`, no installer, no runtime.


<p align="center">
  <img src="docs/plot.png" alt="A plot on the desktop: translucent panel over the wallpaper with a title bar and icons" width="600"><br>
  <em>A plot on the desktop: icons snap into a translucent panel that blends with your wallpaper.</em>
</p>

<p align="center">
  <img src="docs/settings.png" alt="Plots settings window showing the plot list sidebar and the General settings page" width="600"><br>
  <em>Settings: plots in the sidebar, with Appearance and General settings pages.</em>
</p>

## Run it
1. **Quit Fences first.** Both apps move the same desktop icons and will fight each other.
2. Double-click `Plots.exe`. The Plots icon appears in the tray.
3. First run opens **edit mode** with one plot.

## Edit mode
Open it from the tray menu (**Edit plots**), from a plot's right-click menu, or with **Edit layout** in Settings.
- Drag a plot to move it. Drag its edges to resize it.
- Double-click a plot to rename it. Right-click it for colour, opacity, the inbox setting and delete.
- **+ New plot** adds as many plots as you want, on any monitor.
- Click **Done** to save. Icons already sitting under a plot join it.

## Everyday use
- Drag any desktop icon into a plot and it snaps into the plot's grid. Drag it out and it stays where you drop it.
- Drag an icon onto another spot in the same plot to reorder.
- Right-click a plot's **title bar** to open the Plots menu. Right-clicking anywhere else still gives the normal Explorer menu.
- A plot grows taller if it holds more icons than fit.
- **Inbox plot:** pick one plot under *New items & downloads go to this plot*. Any new item that Windows places on the desktop goes there: downloads, saved files, and anything created while Plots was closed. It's marked with a ⤓ arrow. Files you drag onto a specific plot from Explorer, or create there with right-click › New, stay where you dropped them.
- **Move new files from Downloads to the desktop** (optional): when your browser finishes a download, Plots moves it to the desktop so it lands in the inbox. Partial downloads (`.crdownload`, `.part`, …) are ignored.
- **Double-click to hide** (optional, off by default): double-click empty desktop to hide or show all icons and plots. You can also use the tray menu.

## Why it keeps up when Fences doesn't
Plots doesn't keep its own copy of your icons. They stay as real Explorer desktop icons. Plots checks them about twice a second through the documented shell interfaces (`IFolderView`) and fixes any icon that is out of place. That covers new files, renamed files, deletions, Explorer restarts, monitor changes and DPI changes. A file that briefly disappears keeps its plot. A file is only removed from its plot after it's really gone from disk.

## Multi-monitor
Each plot remembers the monitor it's on, by device name, and its position on that monitor. If you unplug that monitor, the plot moves onto the primary monitor until you plug it back in. Sizes follow each monitor's DPI scale.

## Settings and files
- Settings window: open it from the tray icon (single click), or with **Settings…** in any menu.
- Config: `%APPDATA%\Plots\config.json`. Log: `%APPDATA%\Plots\plots.log`.
- Plots turns off Explorer's *Auto arrange icons* and *Align icons to grid*. You can turn this off in Settings, but plots may then look misaligned.

## Building
**Version:** the `VERSION` file holds the version (e.g. `0.2.2`). Bump it when you want a new number. If you push without bumping, CI uses the next free patch number (`0.2.3`, …), so builds never collide.

**GitHub Actions** (`.github/workflows/build.yml`) builds Plots on every push to `main` and publishes a GitHub Release `v<VERSION>`, marked **Latest**, with `Plots.exe` and `Plots-update.exe` attached. Drop `Plots-update.exe` next to a running Plots and it updates itself; Plots also checks Releases on its own. Pull requests only build and test.

**Local build** (`build.bat` does this on Windows): it stamps `<VERSION>-dev`, so a later GitHub release of the same number counts as newer:
```
go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=0.2.2-dev" -o Plots.exe .
```

`.exe` files are git-ignored.
On Linux or macOS, add `GOOS=windows GOARCH=amd64`. Requires Go 1.26+.

The icon, manifest and version info live in `rsrc_windows_*.syso`; keep those committed. To regenerate them after changing `winres/`, run `go run github.com/tc-hib/go-winres@latest make --in winres/winres.json`.

Platform-independent logic (`engine.go`, `render.go`, `config.go`, `wallpaper.go`) has unit tests: `go test ./...`.

## Security
- **Dependabot** (`.github/dependabot.yml`) opens a pull request every Monday when a Go module or GitHub Action has a new version. Related updates are grouped into one PR.
- **Security workflow** (`.github/workflows/security.yml`) runs on every push, every PR and every Monday:
  - **govulncheck** reports known vulnerabilities in the Go modules and Go standard library that Plots actually calls.
  - **CodeQL** does static analysis of the source. Results appear under **Security › Code scanning**.
- Builds always use the newest Go 1.26 patch release, so standard-library security fixes are picked up automatically.

## Layout of the code
| File | What it does |
|---|---|
| `engine.go` | Assignment, drag detection, ordering, inbox, rename tracking, grid layout (pure Go, tested) |
| `render.go` | Draws plot panels: rounded translucent body, title band, title text |
| `config.go` | Config model, load/save |
| `shellview_windows.go` | COM: desktop `IFolderView` (read and move icons, turn off auto-arrange) |
| `desktop_windows.go` | Finds the layer behind the icons (WorkerW / Progman, including the 24H2 layout), monitors |
| `app_windows.go` | Main loop, tick, panels on the desktop layer, hide/show |
| `edit_windows.go` | Edit-mode overlays (move/resize), toolbar, new/delete plot |
| `settings_windows.go` | Settings window |
| `tray_windows.go` | Tray icon, tray menu, plot right-click menu, colour picker |
| `hook_windows.go` | Low-level mouse hook on its own thread (title right-click, double-click hide, drop hints) |
| `downloads_windows.go` | Downloads → desktop mover, start with Windows |
| `panels_windows.go` | Draws plot panels on the desktop layer, behind the icons |
| `wallpaper.go`, `wallpaper_windows.go` | Blends each panel with the wallpaper underneath it so it looks see-through |
| `watcher_windows.go` | Watches the desktop folders and refreshes Explorer when it misses a change |
| `fonts_windows.go` | Lists installed fonts for the Appearance page |
| `.github/workflows/build.yml` | CI: test, vet, build `Plots.exe`, publish a release on every push to `main` |
| `.github/workflows/security.yml`, `.github/dependabot.yml` | govulncheck, CodeQL, Dependabot update PRs |
| `about_windows.go`, `update.go` | About page, GitHub links, update check / download from GitHub Releases |
| `plotmodel.go` | Tabs, sorting rules and their presets |

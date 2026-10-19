# Privacy

Plots does not collect or send any information about you or your computer. Your plots, settings and backups stay on your PC.

The only network connections Plots makes are to GitHub, to check for and download new versions of Plots (`api.github.com` and `github.com`). Plots checks at start-up and every 6 hours. You can turn this off under **Settings › About › Check for updates automatically**, and check by hand instead.

## What Plots changes on your PC

- It moves your desktop icons into your plots. They stay normal Explorer icons and nothing is deleted.
- It can turn off Explorer's *Auto arrange icons* and *Align icons to grid*. This is on by default and can be turned off under **Settings › General**.
- **Start with Windows** (optional) adds a scheduled task named *Plots* that starts it when you sign in.
- **Move finished downloads to the desktop** (optional) moves completed files from your Downloads folder.
- Settings, backups and the log are kept in `%APPDATA%\Plots`.

## Uninstalling

1. In **Settings › General**, turn off **Start with Windows**.
2. Right-click the Plots tray icon and choose **Exit Plots**. Your icons stay on the desktop.
3. Delete `Plots.exe` and the `%APPDATA%\Plots` folder.

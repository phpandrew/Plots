# Code signing policy

Free code signing provided by [SignPath.io](https://about.signpath.io/), certificate by [SignPath Foundation](https://signpath.org/).

Every `Plots.exe` on the [Releases](https://github.com/phpandrew/Plots/releases) page is built by GitHub Actions from the source code in this repository, then signed. Windows shows the publisher as **SignPath Foundation**.

## Team

| Role | Members |
|---|---|
| Authors (commit without further review) | [Andrew Lyle (@phpandrew)](https://github.com/phpandrew) |
| Reviewers (review every outside pull request) | [Andrew Lyle (@phpandrew)](https://github.com/phpandrew) |
| Approvers (approve each signing request) | [Andrew Lyle (@phpandrew)](https://github.com/phpandrew) |

All team members use multi-factor authentication for GitHub and SignPath.

## How a release is signed

1. A push to `main` builds `Plots.exe` on a GitHub-hosted runner and runs the security scans ([SECURITY-FLOW.md](SECURITY-FLOW.md)).
2. If the scans are clean, the build is sent to SignPath, which checks it came from this repository's workflow.
3. An approver reviews and approves the signing request by hand. Nothing is signed automatically.
4. The signed `Plots.exe` is published as a GitHub Release. `Plots-update.exe` is the same signed file under the name the built-in updater looks for.

Each exe's file properties carry the product name **Plots** and the release version.

## Privacy

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

## Setting up signing (maintainers)

1. Apply at [signpath.org/apply](https://signpath.org/apply) with the repository link.
2. Once accepted, in SignPath create a project `plots` with a signing policy `release-signing` that requires manual approval, and this artifact configuration:

   ```xml
   <?xml version="1.0" encoding="utf-8"?>
   <artifact-configuration xmlns="http://signpath.io/artifact-configuration/v1">
     <parameters>
       <parameter name="version" required="true" />
     </parameters>
     <zip-file>
       <pe-file path="Plots.exe" product-name="Plots" product-version="${version}">
         <authenticode-sign />
       </pe-file>
     </zip-file>
   </artifact-configuration>
   ```

3. Create a CI user in SignPath with submitter rights on the policy, and copy its API token.
4. In GitHub (**Settings › Secrets and variables › Actions**):
   - Secret `SIGNPATH_API_TOKEN`: the API token.
   - Variables `SIGNPATH_ORGANIZATION_ID`, `SIGNPATH_PROJECT_SLUG` (`plots`) and `SIGNPATH_POLICY_SLUG` (`release-signing`).

From then on, every release waits for your approval in SignPath (up to 5 hours) and is published signed. Until the variables are set, releases are published unsigned.

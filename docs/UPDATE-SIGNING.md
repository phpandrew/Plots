# Signed updates

Plots updates itself from GitHub Releases. Without a check, anyone who could change a release (a stolen GitHub login, a leaked token), or drop a file next to `Plots.exe`, could get their program run on every PC that updates. So every update is signed, and Plots refuses one that isn't.

This isn't Windows code signing (that needs a paid certificate). It's a free Ed25519 key that only Plots itself checks.

## How it works
- The release workflow builds `Plots-update.exe` and writes `Plots-update.exe.sig` next to it: the version, the file's SHA-256, and an Ed25519 signature over both. The private key is the `PLOTS_SIGNING_KEY` repository secret and never leaves GitHub Actions.
- Plots has the public key built in (`updatekey.go`). Before installing it checks that:
  1. the signature is valid for that key,
  2. the downloaded file's SHA-256 matches the signed one,
  3. the signed version matches the release and is newer than the running version (so an old release can't be pushed back on you).
- It checks again when it swaps the file in, so a `Plots-update.exe` dropped next to `Plots.exe` by something else is moved to `%APPDATA%\Plots\rejected-update.exe.bin` instead of run.
- The release fails if the secret is missing or doesn't match `updatekey.go`, so a release the updater would refuse is never published.
- The release notes also list the SHA-256 of `Plots.exe` for people who download it by hand.

## One-time setup
From the repository root, on your own PC:

```
go run ./tools/sign genkey
```

It writes the public key into `updatekey.go` and prints the private key.

1. In GitHub, open the repository's **Settings › Secrets and variables › Actions › New repository secret**. Name it `PLOTS_SIGNING_KEY` and paste the private key.
2. Keep a copy of the private key in your password manager. Don't save it in the repository.
3. Commit `updatekey.go` and push.

Releases built before this was set up (v0.3.x) don't check signatures, so they update normally to the first signed release. From then on every update is checked.

## If the key is lost or leaked
Run `go run ./tools/sign genkey -force`, update the secret, commit and push. Copies of Plots built with the old key can't verify updates signed with the new one, so those users need to download the new `Plots.exe` once by hand.

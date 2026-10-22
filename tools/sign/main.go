// Command sign makes the update signing key and signs releases.
//
//	go run ./tools/sign genkey               make a key pair: writes updatekey.go, prints the private key
//	go run ./tools/sign sign 0.3.9 file.exe  writes file.exe.sig (private key in $PLOTS_SIGNING_KEY)
//
// Run both from the repository root. See docs/UPDATE-SIGNING.md.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"

	"plots/internal/updatesig"
)

const keyFile = "updatekey.go"

var keyRe = regexp.MustCompile(`updatePublicKey\s*=\s*"([^"]*)"`)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "genkey":
		genkey(len(os.Args) > 2 && os.Args[2] == "-force")
	case "sign":
		if len(os.Args) != 4 {
			usage()
		}
		sign(os.Args[2], os.Args[3])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run ./tools/sign genkey | sign <version> <file.exe>")
	os.Exit(2)
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "sign: "+f+"\n", a...)
	os.Exit(1)
}

// builtInKey is the public key in updatekey.go ("" if none yet).
func builtInKey() string {
	b, err := os.ReadFile(keyFile)
	if err != nil {
		fail("can't read %s (run this from the repository root): %v", keyFile, err)
	}
	m := keyRe.FindSubmatch(b)
	if m == nil {
		fail("%s has no updatePublicKey", keyFile)
	}
	return string(m[1])
}

func genkey(force bool) {
	if builtInKey() != "" && !force {
		fail("%s already has a key. Replacing it means Plots builds with the old key can't update to builds signed with the new one. Use genkey -force if you really mean it.", keyFile)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail("%v", err)
	}
	src := fmt.Sprintf(`package main

// updatePublicKey checks the signature on every update before Plots installs
// it. Made by "go run ./tools/sign genkey"; the private half is the
// PLOTS_SIGNING_KEY secret in GitHub. See docs/UPDATE-SIGNING.md.
const updatePublicKey = %q
`, base64.StdEncoding.EncodeToString(pub))
	if err := os.WriteFile(keyFile, []byte(src), 0o644); err != nil {
		fail("%v", err)
	}
	fmt.Println("Wrote the public key to " + keyFile + ".")
	fmt.Println()
	fmt.Println("Private key (add it as the GitHub secret PLOTS_SIGNING_KEY, and keep a copy in your password manager):")
	fmt.Println()
	fmt.Println(base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	fmt.Println("Don't save it in the repository.")
}

func sign(version, exe string) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("PLOTS_SIGNING_KEY")))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		fail("PLOTS_SIGNING_KEY isn't set or isn't a key made by genkey")
	}
	priv := ed25519.PrivateKey(raw)
	pub := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	if pub != builtInKey() {
		fail("PLOTS_SIGNING_KEY doesn't match the public key in %s, so the build couldn't verify its own updates", keyFile)
	}
	sum, err := updatesig.HashFile(exe)
	if err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(exe+".sig", updatesig.Format(priv, version, sum), 0o644); err != nil {
		fail("%v", err)
	}
	fmt.Printf("signed %s (version %s, sha256 %s)\n", exe, strings.TrimPrefix(version, "v"), sum)
}

// Package updatesig signs and checks Plots updates.
//
// Every release carries Plots-update.exe.sig next to the exe: the version and
// SHA-256 of the exe, signed with an Ed25519 key that only the release
// workflow has (the PLOTS_SIGNING_KEY secret). Plots has the matching public
// key built in and installs an update only if the signature checks out, the
// hash matches the file, and the version is newer than the one running. So a
// tampered download, a file dropped next to Plots.exe, or an old release
// served as "latest" is refused.
package updatesig

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const header = "plots-update"

// Sig is a parsed .sig file.
type Sig struct {
	Version string // without a leading "v"
	SHA256  string // lowercase hex
	Sig     []byte
}

// message is the exact text that's signed.
func message(version, sum string) []byte {
	return []byte(header + "\nversion " + version + "\nsha256 " + sum + "\n")
}

// Format writes a signed .sig file for an exe with the given version and hash.
func Format(priv ed25519.PrivateKey, version, sum string) []byte {
	version = strings.TrimPrefix(version, "v")
	sum = strings.ToLower(sum)
	msg := message(version, sum)
	sig := ed25519.Sign(priv, msg)
	return append(msg, []byte("sig "+base64.StdEncoding.EncodeToString(sig)+"\n")...)
}

// Parse reads a .sig file. It doesn't check the signature; Verify does.
func Parse(b []byte) (*Sig, error) {
	if len(b) > 4096 {
		return nil, errors.New("signature file is too large")
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 4 || lines[0] != header {
		return nil, errors.New("not a Plots update signature")
	}
	s := &Sig{}
	for _, l := range lines[1:] {
		k, v, _ := strings.Cut(l, " ")
		switch k {
		case "version":
			s.Version = v
		case "sha256":
			s.SHA256 = strings.ToLower(v)
		case "sig":
			raw, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return nil, errors.New("signature isn't valid base64")
			}
			s.Sig = raw
		}
	}
	if s.Version == "" || len(s.SHA256) != 64 || len(s.Sig) != ed25519.SignatureSize {
		return nil, errors.New("signature file is incomplete")
	}
	if _, err := hex.DecodeString(s.SHA256); err != nil {
		return nil, errors.New("signature file has a bad hash")
	}
	return s, nil
}

// PublicKey decodes a base64 Ed25519 public key.
func PublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("this build has no update key")
	}
	return ed25519.PublicKey(raw), nil
}

// Verify parses sigFile and checks its signature against pub.
func Verify(pub ed25519.PublicKey, sigFile []byte) (*Sig, error) {
	s, err := Parse(sigFile)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, message(s.Version, s.SHA256), s.Sig) {
		return nil, errors.New("the update's signature doesn't match")
	}
	return s, nil
}

// HashFile is the lowercase hex SHA-256 of a file.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyFile checks that exePath is the file sigFile signs. It returns the
// signed version.
func VerifyFile(pub ed25519.PublicKey, exePath string, sigFile []byte) (string, error) {
	s, err := Verify(pub, sigFile)
	if err != nil {
		return "", err
	}
	sum, err := HashFile(exePath)
	if err != nil {
		return "", err
	}
	if sum != s.SHA256 {
		return "", fmt.Errorf("the downloaded file doesn't match its signature")
	}
	return s.Version, nil
}

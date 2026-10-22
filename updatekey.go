package main

// updatePublicKey checks the signature on every update before Plots installs
// it. Made by "go run ./tools/sign genkey"; the private half is the
// PLOTS_SIGNING_KEY secret in GitHub. See docs/UPDATE-SIGNING.md.
const updatePublicKey = "AiZMivQQf0mgPNqYvnj/kBeNYIQFks+u0WSuG9Z0SZA="

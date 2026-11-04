// Command token is the end-to-end tests' token issuer: the IaC mu-plugin
// accepts its RS256 tokens only on a site whose environment type is local
// (plugin D13).
//
//	go run ./e2e/token config <audience> <whitelist.json>   prints the plugin's config.php
//	go run ./e2e/token token                              prints a token for IAC_AUDIENCE
//
// The key is created on first use in IAC_E2E_KEY, by default
// e2e/build/issuer-key.pem.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	issuer            = "https://issuer.test"
	repositoryID      = 123
	repositoryOwnerID = 456
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: token config <audience> <whitelist.json> | token token")
	}
	key := loadKey()
	switch os.Args[1] {
	case "config":
		if len(os.Args) != 4 {
			fail("usage: token config <audience> <whitelist.json>")
		}
		config(key, os.Args[2], os.Args[3])
	case "token":
		audience := os.Getenv("IAC_AUDIENCE")
		if audience == "" {
			fail("IAC_AUDIENCE is empty")
		}
		fmt.Println(token(key, audience))
	default:
		fail("unknown command " + os.Args[1])
	}
}

func loadKey() *rsa.PrivateKey {
	keyFile := os.Getenv("IAC_E2E_KEY")
	if keyFile == "" {
		keyFile = "e2e/build/issuer-key.pem"
	}
	if data, err := os.ReadFile(keyFile); err == nil {
		block, _ := pem.Decode(data)
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			fail(err.Error())
		}
		return key
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fail(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o755); err != nil {
		fail(err.Error())
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, data, 0o600); err != nil {
		fail(err.Error())
	}
	return key
}

var b64 = base64.RawURLEncoding

// config prints a config.php that trusts this issuer and the whitelist.
func config(key *rsa.PrivateKey, audience, whitelistFile string) {
	whitelist, err := os.ReadFile(whitelistFile)
	if err != nil {
		fail(err.Error())
	}
	settings := map[string]any{
		"install":  "local",
		"audience": audience,
		"trust": map[string]any{
			"repository_id": repositoryID, "repository_owner_id": repositoryOwnerID,
			"read": []string{"develop-plan"}, "write": []string{"develop"},
		},
		"whitelist": json.RawMessage(whitelist),
		"test_issuer": map[string]any{
			"issuer": issuer,
			"jwks": map[string]any{"keys": []map[string]string{{
				"kty": "RSA", "kid": "e2e", "use": "sig",
				"n": b64.EncodeToString(key.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}},
		},
	}
	encoded, _ := json.Marshal(settings)
	fmt.Printf("<?php\nreturn json_decode( <<<'JSON'\n%s\nJSON, true );\n", encoded)
}

// token signs a write token (environment develop) for the audience.
func token(key *rsa.PrivateKey, audience string) string {
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	now := time.Now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "e2e"})
	claims, _ := json.Marshal(map[string]any{
		"iss": issuer, "aud": audience, "iat": now, "nbf": now, "exp": now + 300,
		"repository": "acme/acme-infra", "repository_id": fmt.Sprint(repositoryID), "repository_owner_id": fmt.Sprint(repositoryOwnerID),
		"environment": "develop", "actor": "e2e", "run_id": "1", "sha": "e2e", "jti": hex.EncodeToString(nonce),
	})
	signingInput := b64.EncodeToString(header) + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		fail(err.Error())
	}
	return signingInput + "." + b64.EncodeToString(signature)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

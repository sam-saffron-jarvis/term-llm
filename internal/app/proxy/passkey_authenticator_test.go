package proxy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Synthetic authenticator follows the shared engine's local cryptographic tests.
// No hardware, live service, provider or browser is required for adapter checks.
func localAssertionCredential(t *testing.T) (*ecdsa.PrivateKey, webauthn.Credential) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	return key, webauthn.Credential{ID: []byte("local-credential-id"), PublicKey: publicKey, Flags: webauthn.CredentialFlags{UserPresent: true, UserVerified: true}}
}

func assertionHTTPRequest(t *testing.T, key *ecdsa.PrivateKey, credential webauthn.Credential, session webauthn.SessionData, ceremonyType, origin, rpID string, signCount uint32, uv, validSignature bool) *http.Request {
	t.Helper()
	clientData, err := json.Marshal(map[string]any{"type": ceremonyType, "challenge": session.Challenge, "origin": origin})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte(rpID))
	authenticatorData := make([]byte, 37)
	copy(authenticatorData, rpHash[:])
	authenticatorData[32] = byte(protocol.FlagUserPresent)
	if uv {
		authenticatorData[32] |= byte(protocol.FlagUserVerified)
	}
	binary.BigEndian.PutUint32(authenticatorData[33:], signCount)
	clientHash := sha256.Sum256(clientData)
	signed := append(append([]byte(nil), authenticatorData...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if !validSignature {
		signature[len(signature)-1] ^= 1
	}
	encoded := func(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }
	body, err := json.Marshal(map[string]any{"id": encoded(credential.ID), "rawId": encoded(credential.ID), "type": "public-key", "response": map[string]any{"clientDataJSON": encoded(clientData), "authenticatorData": encoded(authenticatorData), "signature": encoded(signature), "userHandle": nil}, "clientExtensionResults": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "https://backend.invalid/api/auth/login/finish", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func registrationHTTPRequest(t *testing.T, publicKey, credentialID []byte, session webauthn.SessionData, origin, rpID string, uv bool) *http.Request {
	t.Helper()
	clientData, err := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": session.Challenge, "origin": origin})
	if err != nil {
		t.Fatal(err)
	}
	rpHash := sha256.Sum256([]byte(rpID))
	authenticatorData := make([]byte, 0, 37+16+2+len(credentialID)+len(publicKey))
	authenticatorData = append(authenticatorData, rpHash[:]...)
	flags := byte(protocol.FlagUserPresent | protocol.FlagAttestedCredentialData)
	if uv {
		flags |= byte(protocol.FlagUserVerified)
	}
	authenticatorData = append(authenticatorData, flags, 0, 0, 0, 0)
	authenticatorData = append(authenticatorData, make([]byte, 16)...)
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(credentialID)))
	authenticatorData = append(authenticatorData, length...)
	authenticatorData = append(authenticatorData, credentialID...)
	authenticatorData = append(authenticatorData, publicKey...)
	attestationObject, err := cbor.Marshal(map[string]any{"fmt": "none", "authData": authenticatorData, "attStmt": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	body, err := json.Marshal(map[string]any{"id": encode(credentialID), "rawId": encode(credentialID), "type": "public-key", "response": map[string]any{"clientDataJSON": encode(clientData), "attestationObject": encode(attestationObject), "transports": []string{"internal"}}, "clientExtensionResults": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://backend.invalid/api/auth/bootstrap/register/finish", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

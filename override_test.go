package keyfunc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-jwt/jwt"

	"github.com/TernaryInc/keyfunc"
)

const (

	// givenKID is the key ID for the given key with a unique ID.
	givenKID = "givenKID"

	// remoteKID is the key ID for the remote key and given key that has a conflicting key ID.
	remoteKID = "remoteKID"
)

// pseudoJWKS is a data structure used to JSON marshal a JWKS but is not fully featured.
type pseudoJWKS struct {
	Keys []pseudoJSONKey `json:"keys"`
}

// pseudoJSONKey is a data structure that is used to JSON marshal a JWK that is not fully featured.
type pseudoJSONKey struct {
	KID string `json:"kid"`
	KTY string `json:"kty"`
	E   string `json:"e"`
	N   string `json:"n"`
}

// TestNewGiven tests that given keys will be added to a JWKS with a remote resource.
func TestNewGiven(t *testing.T) {

	// Create a temporary directory to serve the JWKS from.
	tempDir, err := ioutil.TempDir("", "*")
	if err != nil {
		t.Errorf("Failed to create a temporary directory.\nError: %s", err.Error())
		t.FailNow()
	}
	defer func() {
		if err = os.RemoveAll(tempDir); err != nil {
			t.Errorf("Failed to remove temporary directory.\nError: %s", err.Error())
			t.FailNow()
		}
	}()

	// Create the JWKS file path.
	jwksFile := filepath.Join(tempDir, jwksFilePath)

	// Create the keys used for this test.
	givenKeys, givenPrivateKeys, jwksBytes, remotePrivateKeys, err := keysAndJWKS()
	if err != nil {
		t.Errorf("Failed to create cryptographic keys for the test.\nError: %s.", err.Error())
		t.FailNow()
	}

	// Write the empty JWKS.
	if err = ioutil.WriteFile(jwksFile, jwksBytes, 0600); err != nil {
		t.Errorf("Failed to write JWKS file to temporary directory.\nError: %s", err.Error())
		t.FailNow()
	}

	// Create the HTTP test server.
	server := httptest.NewServer(http.FileServer(http.Dir(tempDir)))
	defer server.Close()

	// Create testing options.
	testingRefreshErrorHandler := func(err error) {
		panic(fmt.Sprintf("Unhandled JWKS error.\nError: %s", err.Error()))
	}

	// Set the JWKS URL.
	jwksURL := server.URL + jwksFilePath

	// Create the test options.
	options := keyfunc.Options{
		GivenKeys:           givenKeys,
		RefreshErrorHandler: testingRefreshErrorHandler,
	}

	// Get the remote JWKS.
	var jwks *keyfunc.JWKS
	if jwks, err = keyfunc.Get(jwksURL, options); err != nil {
		t.Errorf("Failed to get the JWKS the testing URL.\nError: %s", err.Error())
		t.FailNow()
	}

	// Test the given key with a unique key ID.
	createSignParseValidate(t, givenPrivateKeys, jwks, givenKID, true)

	// Test the given key with a non-unique key ID that should be overwritten.
	createSignParseValidate(t, givenPrivateKeys, jwks, remoteKID, false)

	// Test the remote key that should not have been overwritten.
	createSignParseValidate(t, remotePrivateKeys, jwks, remoteKID, true)

	// Change the JWKS options to overwrite remote keys.
	options.GivenKIDOverride = true
	if jwks, err = keyfunc.Get(jwksURL, options); err != nil {
		t.Errorf("Failed to recreate JWKS.\nError: %s.", err.Error())
		t.FailNow()
	}

	// Test the given key with a unique key ID.
	createSignParseValidate(t, givenPrivateKeys, jwks, givenKID, true)

	// Test the given key with a non-unique key ID that should overwrite the remote key.
	createSignParseValidate(t, givenPrivateKeys, jwks, remoteKID, true)

	// Test the remote key that should have been overwritten.
	createSignParseValidate(t, remotePrivateKeys, jwks, remoteKID, false)
}

// createSignParseValidate creates, signs, parses, and validates a JWT.
func createSignParseValidate(t *testing.T, keys map[string]*rsa.PrivateKey, jwks *keyfunc.JWKS, kid string, shouldValidate bool) {

	// Create the JWT.
	unsignedToken := jwt.New(jwt.SigningMethodRS256)
	unsignedToken.Header[kidAttribute] = kid

	// Sign the JWT.
	jwtB64, err := unsignedToken.SignedString(keys[kid])
	if err != nil {
		t.Errorf("Failed to sign the JWT.\nError: %s.", err.Error())
		t.FailNow()
	}

	// Parse the JWT.
	var token *jwt.Token
	token, err = jwt.Parse(jwtB64, jwks.Keyfunc)
	if err != nil {
		if !shouldValidate && !errors.Is(err, rsa.ErrVerification) {
			return
		}
		t.Errorf("Failed to parse the JWT.\nError: %s", err.Error())
		t.FailNow()

	}
	if !shouldValidate {
		t.Errorf("The token should not have parsed properly.")
		t.FailNow()
	}

	// Validate the JWT.
	if !token.Valid {
		t.Errorf("The JWT is not valid.")
		t.FailNow()
	}
}

// keysAndJWKS creates a couple of cryptographic keys and the remote JWKS associated with them.
func keysAndJWKS() (givenKeys map[string]keyfunc.GivenKey, givenPrivateKeys map[string]*rsa.PrivateKey, jwksBytes []byte, remotePrivateKeys map[string]*rsa.PrivateKey, err error) {

	// Initialize the function's assets.
	const rsaErrMessage = "failed to create RSA key: %w"
	givenKeys = make(map[string]keyfunc.GivenKey)
	givenPrivateKeys = make(map[string]*rsa.PrivateKey)
	remotePrivateKeys = make(map[string]*rsa.PrivateKey)

	// Create a key not in the remote JWKS.
	var key1 *rsa.PrivateKey
	if key1, err = addRSA(givenKeys, givenKID); err != nil {
		return nil, nil, nil, nil, fmt.Errorf(rsaErrMessage, err)
	}
	givenPrivateKeys[givenKID] = key1

	// Create a key to be overwritten by or override the one with the same key ID in the remote JWKS.
	var key2 *rsa.PrivateKey
	if key2, err = addRSA(givenKeys, remoteKID); err != nil {
		return nil, nil, nil, nil, fmt.Errorf(rsaErrMessage, err)
	}
	givenPrivateKeys[remoteKID] = key2

	// Create a key that exists in the remote JWKS.
	var key3 *rsa.PrivateKey
	if key3, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		return nil, nil, nil, nil, fmt.Errorf(rsaErrMessage, err)
	}
	remotePrivateKeys[remoteKID] = key3

	// Create a pseudo-JWKS.
	jwks := pseudoJWKS{Keys: []pseudoJSONKey{{
		KID: remoteKID,
		KTY: "RSA",
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key3.PublicKey.E)).Bytes()),
		N:   base64.RawURLEncoding.EncodeToString(key3.PublicKey.N.Bytes()),
	}}}

	// Marshal the JWKS to JSON.
	if jwksBytes, err = json.Marshal(jwks); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to marshal the JWKS to JSON: %w", err)
	}

	return givenKeys, givenPrivateKeys, jwksBytes, remotePrivateKeys, nil
}

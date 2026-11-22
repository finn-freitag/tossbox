// Package guac builds signed and encrypted login data for Guacamole's JSON
// authentication extension (guacamole-auth-json).
//
// Format: base64( AES-128-CBC( HMAC-SHA256(json) || json ) ), with the
// 128-bit secret key (32 hex chars) used for both HMAC and AES and an all-zero IV.
package guac

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"
)

// Connection is one Guacamole connection.
type Connection struct {
	Protocol   string            `json:"protocol"`
	Parameters map[string]string `json:"parameters"`
}

// Payload is the JSON document the extension expects.
type Payload struct {
	Username    string                `json:"username"`
	Expires     int64                 `json:"expires"` // ms since epoch
	Connections map[string]Connection `json:"connections"`
}

// Encode signs and encrypts the payload with the hex secret key.
func Encode(p Payload, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 16 {
		return "", errors.New("guac: secret key must be 32 hex characters")
	}
	js, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(js)
	plain := append(mac.Sum(nil), js...)

	// PKCS#7 padding.
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decode reverses Encode and verifies the signature (used in tests).
func Decode(data, hexKey string) (*Payload, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 16 {
		return nil, errors.New("guac: bad key")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw)%aes.BlockSize != 0 || len(raw) == 0 {
		return nil, errors.New("guac: bad data")
	}
	block, _ := aes.NewCipher(key)
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(plain, raw)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(plain) {
		return nil, errors.New("guac: bad padding")
	}
	plain = plain[:len(plain)-pad]
	if len(plain) < sha256.Size {
		return nil, errors.New("guac: too short")
	}
	sig, js := plain[:sha256.Size], plain[sha256.Size:]
	mac := hmac.New(sha256.New, key)
	mac.Write(js)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("guac: bad signature")
	}
	var p Payload
	return &p, json.Unmarshal(js, &p)
}

// Target describes the service to connect to.
type Target struct {
	Name     string // connection name shown in Guacamole
	Protocol string // rdp | vnc | ssh
	Host     string
	Port     int
	Username string
	Password string
}

// LoginURL builds the URL that logs the user into Guacamole with exactly one
// connection (GET method of guacamole-auth-json: <guacamole url>/?data=...).
func LoginURL(baseURL, hexKey, guacUser string, t Target, validFor time.Duration) (string, error) {
	params := map[string]string{
		"hostname": t.Host,
		"port":     strconv.Itoa(t.Port),
	}
	switch t.Protocol {
	case "rdp":
		params["username"] = t.Username
		params["password"] = t.Password
		params["ignore-cert"] = "true"
		params["security"] = "any"
		params["resize-method"] = "display-update"
	case "ssh":
		params["username"] = t.Username
		params["password"] = t.Password
	case "vnc":
		params["password"] = t.Password
	default:
		return "", errors.New("guac: unsupported protocol " + t.Protocol)
	}
	data, err := Encode(Payload{
		Username:    guacUser,
		Expires:     time.Now().Add(validFor).UnixMilli(),
		Connections: map[string]Connection{t.Name: {Protocol: t.Protocol, Parameters: params}},
	}, hexKey)
	if err != nil {
		return "", err
	}
	return baseURL + "/?data=" + url.QueryEscape(data), nil
}

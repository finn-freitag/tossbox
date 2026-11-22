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
	"strings"
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
	Name     string // connection name shown in Guacamole (unique per payload)
	Protocol string // rdp | vnc | ssh
	Host     string
	Port     int
	Username string
	Password string
}

// DataSource is the identifier of guacamole-auth-json's data source.
const DataSource = "json"

// ClientID is the identifier Guacamole's web client uses in #/client/<id>
// URLs for a connection: base64(identifier NUL "c" NUL data source). Connections
// from guacamole-auth-json are identified by their name.
func ClientID(connectionName string) string {
	return base64.StdEncoding.EncodeToString([]byte(connectionName + "\x00c\x00" + DataSource))
}

func (t Target) connection() (Connection, error) {
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
		return Connection{}, errors.New("guac: unsupported protocol " + t.Protocol)
	}
	return Connection{Protocol: t.Protocol, Parameters: params}, nil
}

// Login builds the hash route that logs the user into Guacamole and opens the
// selected connection directly: "/client/<id>?data=<signed payload>".
//
// The payload contains all given targets, not only the selected one: when
// another Guacamole tab is already logged in, the web client sends its stored
// token along and Guacamole keeps that existing session (ignoring the new
// data). Including all of the user's connections in every login means the
// deep link still finds its connection in that case.
//
// The route goes into the URL fragment, so the payload (which contains
// device credentials) never reaches server or proxy access logs.
func Login(hexKey, guacUser string, targets []Target, selected string, validFor time.Duration) (string, error) {
	conns := make(map[string]Connection, len(targets))
	for _, t := range targets {
		c, err := t.connection()
		if err != nil {
			return "", err
		}
		conns[t.Name] = c
	}
	if _, ok := conns[selected]; !ok {
		return "", errors.New("guac: selected connection is not in the payload")
	}
	data, err := Encode(Payload{
		Username:    guacUser,
		Expires:     time.Now().Add(validFor).UnixMilli(),
		Connections: conns,
	}, hexKey)
	if err != nil {
		return "", err
	}
	return "/client/" + url.QueryEscape(ClientID(selected)) + "?data=" + url.QueryEscape(data), nil
}

// LoginPath is served by the gateway on the Guacamole host. It drops the auth
// token a previous Guacamole tab stored in localStorage (other open tabs keep
// theirs in memory) and continues to Guacamole with the route in the URL
// fragment, so every login gets a fresh session with its own connections.
const LoginPath = "/.tossbox/guacamole-login"

// LoginURL returns the URL that opens the selected connection. With
// viaHelper, the URL goes through LoginPath on the Guacamole origin (only
// valid when the gateway serves that origin); otherwise straight to Guacamole.
func LoginURL(baseURL, hexKey, guacUser string, targets []Target, selected string, validFor time.Duration, viaHelper bool) (string, error) {
	route, err := Login(hexKey, guacUser, targets, selected, validFor)
	if err != nil {
		return "", err
	}
	if viaHelper {
		u, err := url.Parse(baseURL)
		if err != nil {
			return "", err
		}
		return u.Scheme + "://" + u.Host + LoginPath + "#" + route, nil
	}
	return baseURL + "/#" + route, nil
}

// HelperPage is the HTML served at LoginPath. guacPath is the path Guacamole
// is served under (e.g. "/guacamole"). Guacamole 1.5 stores the token as
// GUAC_AUTH, 1.6+ as GUAC_AUTH_TOKEN.
func HelperPage(guacPath string) []byte {
	target, _ := json.Marshal(strings.TrimRight(guacPath, "/") + "/#")
	return []byte(`<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>Opening Guacamole…</title></head>
<body><noscript>JavaScript is required.</noscript><script>
try { localStorage.removeItem("GUAC_AUTH"); localStorage.removeItem("GUAC_AUTH_TOKEN"); } catch (e) {}
var r = location.hash.slice(1);
location.replace(r.indexOf("/client/") === 0 ? ` + string(target) + ` + r : ` + string(target) + ` + "/");
</script></body></html>`)
}
